package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestMonitorInternalScopeRequired(t *testing.T) {
	router, _ := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	limited, _ := createAPIKeyForTest(t, router, session, "monitor-limited", []string{apiScopeMonitorsWrite})
	body := `{"name":"internal","target":"127.0.0.1:5432","interval_hours":24}`
	resp := apiKeyRequest(t, router, limited, http.MethodPost, "/api/monitors", body, "")
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body, apiScopeMonitorsInternal) {
		t.Fatalf("仅 monitors:write 不应创建内网监控: %d %s", resp.Code, resp.Body)
	}

	trusted, _ := createAPIKeyForTest(t, router, session, "monitor-internal", []string{apiScopeMonitorsWrite, apiScopeMonitorsInternal})
	resp = apiKeyRequest(t, router, trusted, http.MethodPost, "/api/monitors", body, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("具备 monitors:internal 时应允许内网监控: %d %s", resp.Code, resp.Body)
	}
	var created struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(resp.Body), &created); err != nil || created.Data.ID == 0 {
		t.Fatalf("解析监控项失败: %v %s", err, resp.Body)
	}
	path := "/api/monitors/" + strconv.FormatInt(created.Data.ID, 10)
	if got := apiKeyRequest(t, router, limited, http.MethodPost, path+"/check", "", ""); got.Code != http.StatusForbidden {
		t.Fatalf("无内网 scope 不应触发内网监控检查: %d %s", got.Code, got.Body)
	}
	if got := apiKeyRequest(t, router, limited, http.MethodPut, path, body, ""); got.Code != http.StatusForbidden {
		t.Fatalf("无内网 scope 不应修改内网监控: %d %s", got.Code, got.Body)
	}
}
