package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/store"
)

func TestListSystemLogs(t *testing.T) {
	router, st, session := setupLogTestServer(t)
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	entries := []model.SystemLogEntry{
		{Level: "info", Component: "main", Message: "startup", Fields: `{"version":"dev"}`, CreatedAt: base},
		{Level: "warn", Component: "service", Message: "task failed", Fields: `{"task_id":7,"stack":"trace"}`, CreatedAt: base.Add(time.Hour)},
		{Level: "error", Component: "http", Message: "panic recovered", Fields: `{"request_id":"abc"}`, CreatedAt: base.Add(2 * time.Hour)},
	}
	if err := st.AddSystemLogs(entries); err != nil {
		t.Fatal(err)
	}

	w := sessionRequest(t, router, http.MethodGet,
		"/secret-entry/api/system-logs?level=warn&component=service&q=task",
		"", []*http.Cookie{session}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("查询系统日志失败: %d %s", w.Code, w.Body.String())
	}
	var envelope struct {
		Data struct {
			Items         []systemLogItem `json:"items"`
			Total         int64           `json:"total"`
			Components    []string        `json:"components"`
			RetentionDays int             `json:"retention_days"`
			Enabled       bool            `json:"enabled"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("响应不是 JSON: %v %s", err, w.Body.String())
	}
	body := envelope.Data
	if body.Total != 1 || len(body.Items) != 1 {
		t.Fatalf("筛选结果不正确: %+v", body)
	}
	item := body.Items[0]
	if item.Level != "warn" || item.Component != "service" || item.Message != "task failed" {
		t.Fatalf("日志字段不正确: %+v", item)
	}
	if item.Fields["task_id"] != float64(7) || item.Fields["stack"] != "trace" {
		t.Fatalf("附加字段未解析: %+v", item.Fields)
	}
	if len(body.Components) != 3 || body.RetentionDays != 7 {
		t.Fatalf("组件或保留天数不正确: %+v", body)
	}
}

func TestListSystemLogsInvalidFilter(t *testing.T) {
	router, _, session := setupLogTestServer(t)
	cases := []string{
		"/secret-entry/api/system-logs?level=success",
		"/secret-entry/api/system-logs?component=bad%20name",
		"/secret-entry/api/system-logs?from=not-a-time",
		"/secret-entry/api/system-logs?from=2026-08-02T00:00:00Z&to=2026-08-01T00:00:00Z",
	}
	for _, path := range cases {
		w := sessionRequest(t, router, http.MethodGet, path, "", []*http.Cookie{session}, nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s 应返回 400，实际 %d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestClearSystemLogs(t *testing.T) {
	router, st, session := setupLogTestServer(t)
	if err := st.AddSystemLogs([]model.SystemLogEntry{
		{Level: "info", Component: "main", Message: "one", CreatedAt: time.Now().UTC()},
		{Level: "error", Component: "service", Message: "two", CreatedAt: time.Now().UTC()},
	}); err != nil {
		t.Fatal(err)
	}
	w := sessionRequest(t, router, http.MethodDelete, "/secret-entry/api/system-logs", "", []*http.Cookie{session}, map[string]string{
		csrfHeaderName: "test-csrf",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("清空系统日志失败: %d %s", w.Code, w.Body.String())
	}
	var envelope struct {
		Data struct {
			Deleted int64 `json:"deleted"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Deleted != 2 {
		t.Fatalf("删除行数不正确: %d", envelope.Data.Deleted)
	}
	_, total, err := st.ListSystemLogs(store.SystemLogFilter{}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Fatalf("系统日志未清空: total=%d", total)
	}

	audit, _, err := st.ListLogs("settings", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range audit {
		if key, _, ok := i18n.Decode(entry.Message); ok && key == "log.settings.system_logs_cleared" {
			found = true
		}
	}
	if !found {
		t.Fatal("清空操作未写入操作日志")
	}
}

func TestSystemLogsRequiresLogin(t *testing.T) {
	router, _, _ := setupLogTestServer(t)
	w := sessionRequest(t, router, http.MethodGet, "/secret-entry/api/system-logs", "", nil, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("未登录查询应返回 401，实际 %d", w.Code)
	}
}
