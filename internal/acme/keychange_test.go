package acme

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-jose/go-jose/v4"
)

func TestAccountKeyChange(t *testing.T) {
	_, _, _, ts := newTestACMEServer(t, false)
	oldKey, kid := createTestAccount(t, ts)
	newKey := newAccountKey(t)
	target := ts.URL + "/acme/test/key-change"

	oldJWK, _ := json.Marshal(jose.JSONWebKey{Key: oldKey.Public()})
	innerPayload, _ := json.Marshal(map[string]any{
		"account": kid,
		"oldKey":  json.RawMessage(oldJWK),
	})
	opts := (&jose.SignerOptions{}).WithHeader("url", target).WithHeader("jwk", jose.JSONWebKey{Key: newKey.Public()})
	innerSigner, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: newKey}, opts)
	if err != nil {
		t.Fatal(err)
	}
	inner, err := innerSigner.Sign(innerPayload)
	if err != nil {
		t.Fatal(err)
	}
	nonce := getNonce(t, ts, "test")
	body := signJWS(t, oldKey, jose.ES256, target, nonce, kid, []byte(inner.FullSerialize()))
	resp, data := postJWS(t, target, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("keyChange 失败: %d %s", resp.StatusCode, data)
	}

	// 新密钥可以继续访问账户。
	nonce = resp.Header.Get("Replay-Nonce")
	body = signJWS(t, newKey, jose.ES256, kid, nonce, kid, []byte{})
	resp, data = postJWS(t, kid, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("新密钥访问账户失败: %d %s", resp.StatusCode, data)
	}

	// 旧密钥不能再访问账户。
	nonce = resp.Header.Get("Replay-Nonce")
	body = signJWS(t, oldKey, jose.ES256, kid, nonce, kid, []byte{})
	resp, data = postJWS(t, kid, body)
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("旧密钥在 keyChange 后仍可访问账户: %s", data)
	}
}
