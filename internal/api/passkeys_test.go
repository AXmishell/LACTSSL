package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"lactssl/internal/model"
)

// TestPasskeyAvailabilityReflectsRegisteredPasskeys 验证匿名登录页可以查询
// 是否已注册通行密钥：没有凭据时隐藏入口，注册后才展示。
func TestPasskeyAvailabilityReflectsRegisteredPasskeys(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)

	readAvailability := func() bool {
		w := sessionRequest(t, router, http.MethodGet, "/api/auth/passkey/available", "", nil, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("查询通行密钥可用状态失败: %d %s", w.Code, w.Body.String())
		}
		var payload struct {
			Data struct {
				Available bool `json:"available"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatalf("解析响应失败: %v", err)
		}
		return payload.Data.Available
	}

	if readAvailability() {
		t.Fatal("未注册通行密钥时应返回 available=false")
	}

	user, err := st.CreateUser("admin@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreatePasskey(&model.Passkey{
		UserID:       user.ID,
		Name:         "Laptop",
		CredentialID: "credential-availability",
		Credential:   `{"id":"credential-availability"}`,
	}); err != nil {
		t.Fatal(err)
	}
	if !readAvailability() {
		t.Fatal("已注册通行密钥时应返回 available=true")
	}
}
