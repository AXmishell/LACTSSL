package api

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
	"lactssl/internal/logarchive"
	"lactssl/internal/model"
	"lactssl/internal/store"
)

func setupLogTestServer(t *testing.T) (*gin.Engine, *store.Store, *http.Cookie) {
	t.Helper()
	router, st := newSessionTestServerWithStore(t)
	setup := sessionRequest(t, router, http.MethodPost, "/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	return router, st, findCookie(t, setup.Result().Cookies(), sessionCookieName)
}

func TestExportLogsCSV(t *testing.T) {
	router, st, session := setupLogTestServer(t)
	st.AddLogKey("task", 1, "success", "log.task.created", map[string]any{"name": "demo"})
	st.AddLog("task", 2, "error", "=SUM(1,1)")

	w := sessionRequest(t, router, http.MethodGet, "/api/logs/export?format=csv&source=task", "", []*http.Cookie{session}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("导出失败: %d %s", w.Code, w.Body.String())
	}
	if contentType := w.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/csv") {
		t.Fatalf("CSV Content-Type 不正确: %s", contentType)
	}
	if disposition := w.Header().Get("Content-Disposition"); !strings.Contains(disposition, ".csv") {
		t.Fatalf("CSV 文件名不正确: %s", disposition)
	}
	body := w.Body.String()
	if !strings.HasPrefix(body, "\xEF\xBB\xBF") {
		t.Fatal("CSV 缺少 UTF-8 BOM")
	}
	body = strings.TrimPrefix(body, "\xEF\xBB\xBF")
	if !strings.Contains(body, "证书任务") || !strings.Contains(body, "创建证书任务: demo") {
		t.Fatalf("CSV 内容未按当前语言渲染: %s", body)
	}
	if !strings.Contains(body, "'=SUM(1,1)") {
		t.Fatalf("CSV 公式注入未被转义: %s", body)
	}

	reader := csv.NewReader(strings.NewReader(body))
	rows, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("CSV 无法解析: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("CSV 行数不正确: %d", len(rows))
	}
	if len(rows[0]) != 5 {
		t.Fatalf("CSV 列数不正确: %v", rows[0])
	}

	// 导出成功本身应写入审计日志。
	audit, _, err := st.ListLogs("settings", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range audit {
		if key, _, ok := i18n.Decode(entry.Message); ok && key == "log.settings.logs_exported" {
			found = true
		}
	}
	if !found {
		t.Fatal("导出操作未写入审计日志")
	}
}

func TestExportLogsJSONWithFilters(t *testing.T) {
	router, st, session := setupLogTestServer(t)
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	insert := func(source, level, message string, at time.Time) {
		t.Helper()
		if _, err := st.DB.Exec(`INSERT INTO logs(source,ref_id,level,message,created_at) VALUES(?,?,?,?,?)`,
			source, 0, level, message, at); err != nil {
			t.Fatal(err)
		}
	}
	insert("task", "info", "old", base)
	insert("task", "error", "target", base.Add(2*time.Hour))
	insert("deploy", "error", "other source", base.Add(2*time.Hour))

	path := "/api/logs/export?format=json&source=task&level=error&from=" +
		base.Add(time.Hour).Format(time.RFC3339) + "&to=" + base.Add(3*time.Hour).Format(time.RFC3339)
	w := sessionRequest(t, router, http.MethodGet, path, "", []*http.Cookie{session}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("JSON 导出失败: %d %s", w.Code, w.Body.String())
	}
	if contentType := w.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("JSON Content-Type 不正确: %s", contentType)
	}
	var entries []logExportEntry
	if err := json.Unmarshal(w.Body.Bytes(), &entries); err != nil {
		t.Fatalf("JSON 无法解析: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("JSON 筛选结果不正确: %+v", entries)
	}
	if entries[0].Source != "task" || entries[0].Level != "error" || entries[0].Message != "target" {
		t.Fatalf("JSON 字段不正确: %+v", entries[0])
	}
	if _, err := time.Parse(time.RFC3339, entries[0].CreatedAt); err != nil {
		t.Fatalf("created_at 不是 RFC3339: %q", entries[0].CreatedAt)
	}
}

func TestExportLogsInvalidParams(t *testing.T) {
	router, _, session := setupLogTestServer(t)
	cases := []string{
		"/api/logs/export?format=xml",
		"/api/logs/export?level=debug",
		"/api/logs/export?from=not-a-time",
		"/api/logs/export?tz=Bad/Zone",
		"/api/logs/export?from=2026-08-02T00:00:00Z&to=2026-08-01T00:00:00Z",
	}
	for _, path := range cases {
		w := sessionRequest(t, router, http.MethodGet, path, "", []*http.Cookie{session}, nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s 应返回 400，实际 %d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestExportLogsRequiresLogin(t *testing.T) {
	router, _, _ := setupLogTestServer(t)
	w := sessionRequest(t, router, http.MethodGet, "/api/logs/export", "", nil, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("未登录导出应返回 401，实际 %d", w.Code)
	}
}

func TestExportLogsRateLimited(t *testing.T) {
	router, _, session := setupLogTestServer(t)
	for i := 0; i < 5; i++ {
		w := sessionRequest(t, router, http.MethodGet, "/api/logs/export", "", []*http.Cookie{session}, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("第 %d 次导出失败: %d %s", i+1, w.Code, w.Body.String())
		}
	}
	w := sessionRequest(t, router, http.MethodGet, "/api/logs/export", "", []*http.Cookie{session}, nil)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("超过限流阈值应返回 429，实际 %d", w.Code)
	}
}

func TestListLogsSearch(t *testing.T) {
	router, st, session := setupLogTestServer(t)
	st.AddLogKey("task", 1, "success", "log.task.created", map[string]any{"name": "demo.example"})
	st.AddLog("deploy", 2, "error", "connection refused")
	if err := st.AddSystemLogs([]model.SystemLogEntry{{
		Level: "info", Component: "main", Message: "demo.example startup", CreatedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}

	w := sessionRequest(t, router, http.MethodGet, "/api/logs?q=demo.example", "", []*http.Cookie{session}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("查询操作日志失败: %d %s", w.Code, w.Body.String())
	}
	var envelope struct {
		Data struct {
			Items []struct {
				Source  string `json:"source"`
				Message string `json:"message"`
			} `json:"items"`
			Total int64 `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Total != 1 || len(envelope.Data.Items) != 1 {
		t.Fatalf("关键字搜索结果不正确: total=%d items=%+v", envelope.Data.Total, envelope.Data.Items)
	}
	if envelope.Data.Items[0].Source != "task" {
		t.Fatalf("关键字搜索命中了错误的日志: %+v", envelope.Data.Items[0])
	}

	// 导出与列表共用筛选参数，关键字必须同样生效。
	w = sessionRequest(t, router, http.MethodGet, "/api/logs/export?format=json&q=refused", "", []*http.Cookie{session}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("按关键字导出失败: %d %s", w.Code, w.Body.String())
	}
	var entries []logExportEntry
	if err := json.Unmarshal(w.Body.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Source != "deploy" {
		t.Fatalf("按关键字导出结果不正确: %+v", entries)
	}

	tooLong := strings.Repeat("a", 101)
	w = sessionRequest(t, router, http.MethodGet, "/api/logs?q="+tooLong, "", []*http.Cookie{session}, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("超长关键字应返回 400，实际 %d %s", w.Code, w.Body.String())
	}
}

func TestClearLogs(t *testing.T) {
	router, st, session := setupLogTestServer(t)
	_, before, err := st.ListLogs("", 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	st.AddLogKey("task", 1, "info", "log.task.created", map[string]any{"name": "one"})
	st.AddLogKey("deploy", 2, "error", "log.deploy.failed", map[string]any{"name": "two"})
	if err := st.AddSystemLogs([]model.SystemLogEntry{{
		Level: "info", Component: "main", Message: "keep me", CreatedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}

	w := sessionRequest(t, router, http.MethodDelete, "/api/logs", "", []*http.Cookie{session}, map[string]string{
		csrfHeaderName: "test-csrf",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("清空操作日志失败: %d %s", w.Code, w.Body.String())
	}
	var envelope struct {
		Data struct {
			Deleted int64 `json:"deleted"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Deleted != before+2 {
		t.Fatalf("删除行数不正确: %d (before=%d)", envelope.Data.Deleted, before)
	}

	// 清空动作本身会写入一条审计日志，因此剩余 1 条操作日志。
	remaining, total, err := st.ListLogs("", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(remaining) != 1 {
		t.Fatalf("操作日志清空结果不正确: total=%d list=%+v", total, remaining)
	}
	if key, _, ok := i18n.Decode(remaining[0].Message); !ok || key != "log.settings.logs_cleared" {
		t.Fatalf("清空操作未写入审计日志: %+v", remaining[0])
	}

	// 系统日志不受影响。
	_, systemTotal, err := st.ListSystemLogs(store.SystemLogFilter{}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if systemTotal != 1 {
		t.Fatalf("清空操作日志误删了系统日志: total=%d", systemTotal)
	}
}

func TestClearLogsRequiresWriteScope(t *testing.T) {
	router, _, session := setupLogTestServer(t)
	readToken, _ := createAPIKeyForTest(t, router, session, "log-reader", []string{apiScopeLogsRead})
	resp := apiKeyRequest(t, router, readToken, http.MethodDelete, "/api/logs", "", apiKeyHeader)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("只读密钥清空操作日志应返回 403，实际 %d %s", resp.Code, resp.Body)
	}

	writeToken, _ := createAPIKeyForTest(t, router, session, "log-cleaner", []string{apiScopeLogsWrite})
	resp = apiKeyRequest(t, router, writeToken, http.MethodDelete, "/api/logs", "", apiKeyHeader)
	if resp.Code != http.StatusOK {
		t.Fatalf("logs:write 密钥清空操作日志失败: %d %s", resp.Code, resp.Body)
	}
}

func TestClearLogsArchivesBeforeDelete(t *testing.T) {
	router, st, session := setupLogTestServer(t)
	root := t.TempDir()
	st.Archiver = logarchive.New(st, logarchive.Options{RootDir: root, Compress: true})

	st.AddLogKey("task", 1, "info", "log.task.created", map[string]any{"name": "one"})
	st.AddLogKey("deploy", 2, "error", "log.deploy.failed", map[string]any{"name": "two"})

	w := sessionRequest(t, router, http.MethodDelete, "/api/logs", "", []*http.Cookie{session}, map[string]string{
		csrfHeaderName: "test-csrf",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("清空操作日志失败: %d %s", w.Code, w.Body.String())
	}
	var envelope struct {
		Data struct {
			Deleted  int64 `json:"deleted"`
			Archived *struct {
				ID    string `json:"id"`
				File  string `json:"file"`
				Rows  int64  `json:"rows"`
				Bytes int64  `json:"bytes"`
			} `json:"archived"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Deleted < 2 || envelope.Data.Archived == nil {
		t.Fatalf("清空接口未返回归档结果: %+v", envelope.Data)
	}
	if envelope.Data.Archived.Rows != envelope.Data.Deleted {
		t.Fatalf("归档行数与删除行数不一致: %+v deleted=%d", envelope.Data.Archived, envelope.Data.Deleted)
	}
	dataPath := filepath.Join(root, filepath.FromSlash(envelope.Data.Archived.File))
	if _, err := os.Stat(dataPath); err != nil {
		t.Fatalf("归档文件不存在: %v", err)
	}
}

func TestClearLogsArchiveFailureKeepsLogs(t *testing.T) {
	router, st, session := setupLogTestServer(t)
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	st.Archiver = logarchive.New(st, logarchive.Options{RootDir: blocker, Compress: true})
	st.AddLogKey("task", 1, "info", "log.task.created", map[string]any{"name": "keep"})

	w := sessionRequest(t, router, http.MethodDelete, "/api/logs", "", []*http.Cookie{session}, map[string]string{
		csrfHeaderName: "test-csrf",
	})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("归档失败应阻止清空，实际 %d %s", w.Code, w.Body.String())
	}
	_, total, err := st.ListLogs("", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total == 0 {
		t.Fatal("归档失败后日志被误删")
	}
}
