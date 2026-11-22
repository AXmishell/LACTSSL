package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
	"lactssl/internal/auth"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/service"
	"lactssl/internal/store"
)

func setupTaskLogTestServer(t *testing.T) (*gin.Engine, *store.Store, *http.Cookie) {
	t.Helper()
	st := newSessionTestStore(t)
	gin.SetMode(gin.TestMode)
	s := New(
		st,
		auth.New([]byte("jwt-secret")),
		service.New(st),
		"secret-entry",
		fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(sessionIndexHTML)}},
		nil,
	)
	s.SetSetupToken("task-log-setup-token")
	router := s.Router()
	setup := sessionRequest(t, router, http.MethodPost, "/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil,
		map[string]string{setupTokenHeader: "task-log-setup-token"})
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	return router, st, findCookie(t, setup.Result().Cookies(), sessionCookieName)
}

func TestClearTaskLogs(t *testing.T) {
	router, st, session := setupTaskLogTestServer(t)
	task := &model.Task{
		Name:      "clear-logs",
		Domains:   []string{"clear.example.com"},
		Challenge: "dns01",
		CA:        "letsencrypt",
		KeyType:   "ecdsa",
		Status:    "pending",
	}
	if err := st.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	other := &model.Task{
		Name:      "keep-logs",
		Domains:   []string{"keep.example.com"},
		Challenge: "dns01",
		CA:        "letsencrypt",
		KeyType:   "ecdsa",
		Status:    "pending",
	}
	if err := st.CreateTask(other); err != nil {
		t.Fatal(err)
	}

	st.AddLogKey("task", task.ID, "success", "log.task.created", map[string]any{"name": task.Name})
	st.AddLog("deploy", task.ID, "error", "deploy failed")
	st.AddLogKey("task", other.ID, "info", "log.task.created", map[string]any{"name": other.Name})
	st.AddLog("deploy", 0, "info", "global deploy")

	w := sessionRequest(t, router, http.MethodDelete,
		fmt.Sprintf("/api/tasks/%d/logs", task.ID), "", []*http.Cookie{session},
		map[string]string{csrfHeaderName: "test-csrf"})
	if w.Code != http.StatusOK {
		t.Fatalf("清理任务日志失败: %d %s", w.Code, w.Body.String())
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

	for _, source := range []string{"task", "deploy"} {
		list, err := st.ListLogsByRef(source, task.ID, 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 0 {
			t.Fatalf("%s 日志未清理: %+v", source, list)
		}
	}
	kept, err := st.ListLogsByRef("task", other.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 {
		t.Fatalf("误删了其它任务日志: %+v", kept)
	}

	// 清理动作写入 settings 来源的审计日志，且不会再次出现在任务执行日志中。
	settingsLogs, _, err := st.ListLogs("settings", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range settingsLogs {
		if key, _, ok := i18n.Decode(entry.Message); ok && key == "log.settings.task_logs_cleared" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("清理任务日志未写入审计日志: %+v", settingsLogs)
	}
}

func TestClearTaskLogsRequiresTaskWriteScope(t *testing.T) {
	router, st, session := setupTaskLogTestServer(t)
	task := &model.Task{
		Name:      "scoped-logs",
		Domains:   []string{"scoped.example.com"},
		Challenge: "dns01",
		CA:        "letsencrypt",
		KeyType:   "ecdsa",
		Status:    "pending",
	}
	if err := st.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	st.AddLog("task", task.ID, "info", "clear me")
	path := fmt.Sprintf("/api/tasks/%d/logs", task.ID)

	readToken, _ := createAPIKeyForTest(t, router, session, "task-log-reader", []string{apiScopeTasksRead})
	resp := apiKeyRequest(t, router, readToken, http.MethodDelete, path, "", apiKeyHeader)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("只读密钥清理任务日志应返回 403，实际 %d %s", resp.Code, resp.Body)
	}

	writeToken, _ := createAPIKeyForTest(t, router, session, "task-log-writer", []string{apiScopeTasksWrite})
	resp = apiKeyRequest(t, router, writeToken, http.MethodDelete, path, "", apiKeyHeader)
	if resp.Code != http.StatusOK {
		t.Fatalf("tasks:write 密钥清理任务日志失败: %d %s", resp.Code, resp.Body)
	}
}
