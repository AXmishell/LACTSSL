package api

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
	"lactssl/internal/store"
)

func setupLogTestServer(t *testing.T) (*gin.Engine, *store.Store, *http.Cookie) {
	t.Helper()
	router, st := newSessionTestServerWithStore(t)
	setup := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/setup",
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

	w := sessionRequest(t, router, http.MethodGet, "/secret-entry/api/logs/export?format=csv&source=task", "", []*http.Cookie{session}, nil)
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

	path := "/secret-entry/api/logs/export?format=json&source=task&level=error&from=" +
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
		"/secret-entry/api/logs/export?format=xml",
		"/secret-entry/api/logs/export?level=debug",
		"/secret-entry/api/logs/export?from=not-a-time",
		"/secret-entry/api/logs/export?tz=Bad/Zone",
		"/secret-entry/api/logs/export?from=2026-08-02T00:00:00Z&to=2026-08-01T00:00:00Z",
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
	w := sessionRequest(t, router, http.MethodGet, "/secret-entry/api/logs/export", "", nil, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("未登录导出应返回 401，实际 %d", w.Code)
	}
}

func TestExportLogsRateLimited(t *testing.T) {
	router, _, session := setupLogTestServer(t)
	for i := 0; i < 5; i++ {
		w := sessionRequest(t, router, http.MethodGet, "/secret-entry/api/logs/export", "", []*http.Cookie{session}, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("第 %d 次导出失败: %d %s", i+1, w.Code, w.Body.String())
		}
	}
	w := sessionRequest(t, router, http.MethodGet, "/secret-entry/api/logs/export", "", []*http.Cookie{session}, nil)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("超过限流阈值应返回 429，实际 %d", w.Code)
	}
}
