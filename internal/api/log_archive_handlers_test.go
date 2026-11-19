package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"lactssl/internal/logarchive"
	"lactssl/internal/model"
	"lactssl/internal/store"
)

type logArchiveListEnvelope struct {
	Data struct {
		Items []struct {
			ArchiveID string `json:"archive_id"`
			Category  string `json:"category"`
			Reason    string `json:"reason"`
			CreatedAt string `json:"created_at"`
			RowCount  int64  `json:"row_count"`
			File      struct {
				Name   string `json:"name"`
				Format string `json:"format"`
				Bytes  int64  `json:"bytes"`
				SHA256 string `json:"sha256"`
			} `json:"file"`
			Operator struct {
				UserID int64  `json:"user_id"`
				Email  string `json:"email"`
			} `json:"operator"`
			Missing bool `json:"missing"`
		} `json:"items"`
		Total   int    `json:"total"`
		Page    int    `json:"page"`
		Size    int    `json:"size"`
		Enabled bool   `json:"enabled"`
		Dir     string `json:"dir"`
	} `json:"data"`
}

func createArchivesForTest(t *testing.T, router http.Handler, st *store.Store, session *http.Cookie) {
	t.Helper()
	st.AddLogKey("task", 1, "info", "log.task.created", map[string]any{"name": "demo"})
	st.AddLogKey("deploy", 2, "error", "log.deploy.failed", map[string]any{"name": "demo2"})
	w := sessionRequest(t, router, http.MethodDelete, "/api/logs", "", []*http.Cookie{session}, map[string]string{
		csrfHeaderName: "test-csrf",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("创建操作日志归档失败: %d %s", w.Code, w.Body.String())
	}

	if err := st.AddSystemLogs([]model.SystemLogEntry{
		{Level: "info", Component: "main", Message: "startup"},
		{Level: "warn", Component: "service", Message: "slow"},
	}); err != nil {
		t.Fatal(err)
	}
	w = sessionRequest(t, router, http.MethodDelete, "/api/system-logs", "", []*http.Cookie{session}, map[string]string{
		csrfHeaderName: "test-csrf",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("创建系统日志归档失败: %d %s", w.Code, w.Body.String())
	}
}

func TestLogArchiveListAndDownload(t *testing.T) {
	router, st, session := setupLogTestServer(t)
	createArchivesForTest(t, router, st, session)

	w := sessionRequest(t, router, http.MethodGet, "/api/log-archives", "", []*http.Cookie{session}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("查询归档失败: %d %s", w.Code, w.Body.String())
	}
	var payload logArchiveListEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Data.Enabled || payload.Data.Total != 2 || len(payload.Data.Items) != 2 {
		t.Fatalf("归档列表不正确: %+v", payload.Data)
	}
	if payload.Data.Dir == "" {
		t.Fatal("归档列表未返回目录")
	}
	for _, item := range payload.Data.Items {
		if item.ArchiveID == "" || item.File.Name == "" || item.File.SHA256 == "" || item.RowCount == 0 {
			t.Fatalf("归档元数据不完整: %+v", item)
		}
	}

	// 分类筛选只返回对应日志。
	w = sessionRequest(t, router, http.MethodGet, "/api/log-archives?category=runtime", "", []*http.Cookie{session}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("筛选归档失败: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.Total != 1 || payload.Data.Items[0].Category != model.LogCategoryRuntime {
		t.Fatalf("分类筛选结果不正确: %+v", payload.Data)
	}

	id := payload.Data.Items[0].ArchiveID
	w = sessionRequest(t, router, http.MethodGet, "/api/log-archives/"+id, "", []*http.Cookie{session}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("查询单个归档失败: %d %s", w.Code, w.Body.String())
	}

	w = sessionRequest(t, router, http.MethodGet, "/api/log-archives/"+id+"/download", "", []*http.Cookie{session}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("下载归档失败: %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("下载归档缺少 no-store: %s", w.Header().Get("Cache-Control"))
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("下载归档缺少 nosniff: %s", w.Header().Get("X-Content-Type-Options"))
	}
	if !strings.Contains(w.Header().Get("Content-Disposition"), ".ndjson.gz") {
		t.Fatalf("下载文件名不正确: %s", w.Header().Get("Content-Disposition"))
	}
	if !bytes.HasPrefix(w.Body.Bytes(), []byte{0x1f, 0x8b}) {
		t.Fatalf("下载内容不是 gzip: %x", w.Body.Bytes()[:2])
	}
}

func TestLogArchiveDeleteAndMissingFile(t *testing.T) {
	router, st, session := setupLogTestServer(t)
	createArchivesForTest(t, router, st, session)

	w := sessionRequest(t, router, http.MethodGet, "/api/log-archives?category=operation", "", []*http.Cookie{session}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("查询归档失败: %d %s", w.Code, w.Body.String())
	}
	var payload logArchiveListEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.Total != 1 {
		t.Fatalf("操作日志归档数量不正确: %d", payload.Data.Total)
	}
	id := payload.Data.Items[0].ArchiveID
	archiver, ok := st.Archiver.(*logarchive.Service)
	if !ok {
		t.Fatal("测试归档服务类型不正确")
	}
	entry, found, err := archiver.Get(id)
	if err != nil || !found {
		t.Fatalf("查询归档元数据失败: found=%v err=%v", found, err)
	}
	if err := os.Remove(entry.DataPath); err != nil {
		t.Fatal(err)
	}
	w = sessionRequest(t, router, http.MethodGet, "/api/log-archives/"+id+"/download", "", []*http.Cookie{session}, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("归档文件缺失应返回 404，实际 %d %s", w.Code, w.Body.String())
	}

	w = sessionRequest(t, router, http.MethodDelete, "/api/log-archives/"+id, "", []*http.Cookie{session}, map[string]string{
		csrfHeaderName: "test-csrf",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("删除归档失败: %d %s", w.Code, w.Body.String())
	}
	w = sessionRequest(t, router, http.MethodGet, "/api/log-archives/"+id, "", []*http.Cookie{session}, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("删除后查询应返回 404，实际 %d %s", w.Code, w.Body.String())
	}
}

func TestLogArchiveAPIKeyScopes(t *testing.T) {
	router, st, session := setupLogTestServer(t)
	createArchivesForTest(t, router, st, session)

	readToken, _ := createAPIKeyForTest(t, router, session, "log-archive-reader", []string{apiScopeLogsRead})
	resp := apiKeyRequest(t, router, readToken, http.MethodGet, "/api/log-archives", "", apiKeyHeader)
	if resp.Code != http.StatusOK {
		t.Fatalf("logs:read 密钥应允许查看归档: %d %s", resp.Code, resp.Body)
	}

	systemToken, _ := createAPIKeyForTest(t, router, session, "system-log-archive-reader", []string{apiScopeSystemLogsRead})
	resp = apiKeyRequest(t, router, systemToken, http.MethodGet, "/api/log-archives", "", apiKeyHeader)
	if resp.Code != http.StatusOK {
		t.Fatalf("system_logs:read 密钥应允许查看归档: %d %s", resp.Code, resp.Body)
	}

	settingsToken, _ := createAPIKeyForTest(t, router, session, "archive-settings-reader", []string{apiScopeSettingsRead})
	resp = apiKeyRequest(t, router, settingsToken, http.MethodGet, "/api/log-archives", "", apiKeyHeader)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("无关权限不应查看归档: %d %s", resp.Code, resp.Body)
	}

	var payload logArchiveListEnvelope
	list := apiKeyRequest(t, router, readToken, http.MethodGet, "/api/log-archives?category=operation", "", apiKeyHeader)
	if err := json.Unmarshal([]byte(list.Body), &payload); err != nil || len(payload.Data.Items) == 0 {
		t.Fatalf("解析归档列表失败: %v %s", err, list.Body)
	}
	id := payload.Data.Items[0].ArchiveID

	resp = apiKeyRequest(t, router, readToken, http.MethodDelete, "/api/log-archives/"+id, "", apiKeyHeader)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("只读密钥不应删除归档: %d %s", resp.Code, resp.Body)
	}

	writeToken, _ := createAPIKeyForTest(t, router, session, "log-archive-writer", []string{apiScopeLogsWrite})
	resp = apiKeyRequest(t, router, writeToken, http.MethodDelete, "/api/log-archives/"+id, "", apiKeyHeader)
	if resp.Code != http.StatusOK {
		t.Fatalf("logs:write 密钥应允许删除归档: %d %s", resp.Code, resp.Body)
	}
}
