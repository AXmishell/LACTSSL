package acme

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/go-jose/go-jose/v4"
)

func TestNewOrderReplacesCertificate(t *testing.T) {
	_, st, _, ts, ca := newOrderTestServer(t)
	key, kid := createTestAccount(t, ts)
	account, err := st.GetACMEAccountByKid(kid)
	if err != nil {
		t.Fatal(err)
	}
	certificate, leaf := issueTestCertificate(t, ca)
	certificate.ACMEAccountID = account.ID
	certificate.PrivateCAID = ca.ID
	if err := st.CreateCertificate(certificate); err != nil {
		t.Fatal(err)
	}
	certID := certificateID(leaf)
	target := ts.URL + "/acme/test/new-order"
	payload, _ := json.Marshal(map[string]any{
		"identifiers": []map[string]string{{"type": "dns", "value": "localhost"}},
		"replaces":    certID,
	})
	nonce := getNonce(t, ts, "test")
	body := signJWS(t, key, jose.ES256, target, nonce, kid, payload)
	resp, data := postJWS(t, target, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("带 replaces 的订单创建失败: %d %s", resp.StatusCode, data)
	}
	var order orderView
	if err := json.Unmarshal(data, &order); err != nil {
		t.Fatal(err)
	}
	if order.Replaces != certID {
		t.Fatalf("订单响应未反映 replaces: %+v", order)
	}

	// 同一证书已有未失效的替换订单，第二次应返回 409 alreadyReplaced。
	nonce = getNonce(t, ts, "test")
	body = signJWS(t, key, jose.ES256, target, nonce, kid, payload)
	resp, data = postJWS(t, target, body)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(string(data), "alreadyReplaced") {
		t.Fatalf("重复替换未被拒绝: %d %s", resp.StatusCode, data)
	}
}

func TestNewOrderReplacesRejectsOtherAccount(t *testing.T) {
	_, st, _, ts, ca := newOrderTestServer(t)
	_, kid := createTestAccount(t, ts)
	account, _ := st.GetACMEAccountByKid(kid)
	certificate, leaf := issueTestCertificate(t, ca)
	certificate.ACMEAccountID = account.ID
	certificate.PrivateCAID = ca.ID
	if err := st.CreateCertificate(certificate); err != nil {
		t.Fatal(err)
	}
	certID := certificateID(leaf)

	otherKey, otherKid := createTestAccount(t, ts)
	target := ts.URL + "/acme/test/new-order"
	payload, _ := json.Marshal(map[string]any{
		"identifiers": []map[string]string{{"type": "dns", "value": "localhost"}},
		"replaces":    certID,
	})
	nonce := getNonce(t, ts, "test")
	body := signJWS(t, otherKey, jose.ES256, target, nonce, otherKid, payload)
	resp, data := postJWS(t, target, body)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(data), "rejectedIdentifier") {
		t.Fatalf("其他账户的替换订单未被拒绝: %d %s", resp.StatusCode, data)
	}
}
