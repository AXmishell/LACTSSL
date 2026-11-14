package api

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"lactssl/internal/i18n"
	"lactssl/internal/model"
)

func seedSystemLogs(t *testing.T, st interface {
	AddSystemLogs([]model.SystemLogEntry) error
}) time.Time {
	t.Helper()
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	entries := []model.SystemLogEntry{
		{Level: "info", Component: "main", Message: "startup", Fields: `{"version":"dev"}`, CreatedAt: base},
		{Level: "warn", Component: "service", Message: "task failed", Fields: `{"task_id":7}`, CreatedAt: base.Add(time.Hour)},
		{Level: "error", Component: "http", Message: "=panic", Fields: `{"request_id":"abc"}`, CreatedAt: base.Add(2 * time.Hour)},
	}
	if err := st.AddSystemLogs(entries); err != nil {
		t.Fatal(err)
	}
	return base
}

func TestExportSystemLogsCSV(t *testing.T) {
	router, st, session := setupLogTestServer(t)
	seedSystemLogs(t, st)

	w := sessionRequest(t, router, http.MethodGet,
		"/secret-entry/api/system-logs/export?format=csv&level=warn&component=service&q=task",
		"", []*http.Cookie{session}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("导出失败: %d %s", w.Code, w.Body.String())
	}
	if contentType := w.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/csv") {
		t.Fatalf("CSV Content-Type 不正确: %s", contentType)
	}
	if disposition := w.Header().Get("Content-Disposition"); !strings.Contains(disposition, "lactssl-system-logs-") {
		t.Fatalf("CSV 文件名不正确: %s", disposition)
	}
	body := w.Body.String()
	if !strings.HasPrefix(body, "\xEF\xBB\xBF") {
		t.Fatal("CSV 缺少 UTF-8 BOM")
	}
	body = strings.TrimPrefix(body, "\xEF\xBB\xBF")
	if !strings.Contains(body, "组件") || !strings.Contains(body, "附加字段") || !strings.Contains(body, "调度服务") {
		t.Fatalf("CSV 内容未按当前语言渲染: %s", body)
	}
	reader := csv.NewReader(strings.NewReader(body))
	rows, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("CSV 无法解析: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("CSV 行数不正确: %d", len(rows))
	}
	if len(rows[0]) != 5 {
		t.Fatalf("CSV 列数不正确: %v", rows[0])
	}
	if rows[1][2] != "调度服务" || !strings.Contains(rows[1][4], `"task_id":7`) {
		t.Fatalf("CSV 字段不正确: %v", rows[1])
	}

	audit, _, err := st.ListLogs("settings", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range audit {
		if key, _, ok := i18n.Decode(entry.Message); ok && key == "log.settings.system_logs_exported" {
			found = true
		}
	}
	if !found {
		t.Fatal("导出操作未写入操作日志")
	}
}

func TestExportSystemLogsJSON(t *testing.T) {
	router, st, session := setupLogTestServer(t)
	seedSystemLogs(t, st)

	w := sessionRequest(t, router, http.MethodGet,
		"/secret-entry/api/system-logs/export?format=json&level=error&tz=UTC",
		"", []*http.Cookie{session}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("JSON 导出失败: %d %s", w.Code, w.Body.String())
	}
	if contentType := w.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("JSON Content-Type 不正确: %s", contentType)
	}
	var entries []systemLogExportEntry
	if err := json.Unmarshal(w.Body.Bytes(), &entries); err != nil {
		t.Fatalf("JSON 无法解析: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("JSON 筛选结果不正确: %+v", entries)
	}
	entry := entries[0]
	if entry.Level != "error" || entry.Component != "http" || entry.Message != "=panic" {
		t.Fatalf("JSON 字段不正确: %+v", entry)
	}
	if entry.Fields["request_id"] != "abc" {
		t.Fatalf("JSON 附加字段未解析: %+v", entry.Fields)
	}
	if _, err := time.Parse(time.RFC3339, entry.CreatedAt); err != nil {
		t.Fatalf("created_at 不是 RFC3339: %q", entry.CreatedAt)
	}
}

func TestExportSystemLogsInvalidParams(t *testing.T) {
	router, _, session := setupLogTestServer(t)
	cases := []string{
		"/secret-entry/api/system-logs/export?format=xml",
		"/secret-entry/api/system-logs/export?level=success",
		"/secret-entry/api/system-logs/export?from=not-a-time",
		"/secret-entry/api/system-logs/export?tz=Bad/Zone",
		"/secret-entry/api/system-logs/export?from=2026-08-02T00:00:00Z&to=2026-08-01T00:00:00Z",
	}
	for _, path := range cases {
		w := sessionRequest(t, router, http.MethodGet, path, "", []*http.Cookie{session}, nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s 应返回 400，实际 %d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestExportSystemLogsRequiresLogin(t *testing.T) {
	router, _, _ := setupLogTestServer(t)
	w := sessionRequest(t, router, http.MethodGet, "/secret-entry/api/system-logs/export", "", nil, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("未登录导出应返回 401，实际 %d", w.Code)
	}
}

func TestExportSystemLogsCSVFormulaSafe(t *testing.T) {
	router, st, session := setupLogTestServer(t)
	if err := st.AddSystemLogs([]model.SystemLogEntry{{
		Level: "error", Component: "main", Message: "=SUM(1,1)", CreatedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}
	w := sessionRequest(t, router, http.MethodGet,
		"/secret-entry/api/system-logs/export?format=csv",
		"", []*http.Cookie{session}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("导出失败: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "'=SUM(1,1)") {
		t.Fatalf("CSV 公式注入未被转义: %s", w.Body.String())
	}
}
