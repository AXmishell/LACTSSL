package acme

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func ariCertID(cert *x509.Certificate) string {
	serial := cert.SerialNumber.Bytes()
	if len(serial) == 0 || serial[0]&0x80 != 0 {
		serial = append([]byte{0}, serial...)
	}
	return base64.RawURLEncoding.EncodeToString(cert.AuthorityKeyId) + "." + base64.RawURLEncoding.EncodeToString(serial)
}

func TestRenewalInfo(t *testing.T) {
	_, st, _, ts, ca := newOrderTestServer(t)
	certificate, leaf := issueTestCertificate(t, ca)
	if err := st.CreateCertificate(certificate); err != nil {
		t.Fatal(err)
	}
	url := ts.URL + "/acme/test/renewal-info/" + ariCertID(leaf)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ARI 请求失败: %d", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Fatal("ARI 响应应包含 Retry-After")
	}
	var view renewalInfoView
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	if !view.SuggestedWindow.End.After(view.SuggestedWindow.Start) {
		t.Fatalf("ARI 建议窗口不正确: %+v", view.SuggestedWindow)
	}
	if view.SuggestedWindow.Start.After(certificate.NotAfter) {
		t.Fatal("ARI 建议窗口不应晚于证书到期时间")
	}

	if err := st.RevokeCertificate(certificate.ID, 1, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	resp2, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var revoked renewalInfoView
	if err := json.NewDecoder(resp2.Body).Decode(&revoked); err != nil {
		t.Fatal(err)
	}
	if !revoked.SuggestedWindow.End.Before(time.Now()) {
		t.Fatalf("已撤销证书的 ARI 窗口应位于过去: %+v", revoked.SuggestedWindow)
	}
}

func TestRenewalInfoDirectoryField(t *testing.T) {
	_, _, _, ts := newTestACMEServer(t, false)
	resp, err := http.Get(ts.URL + "/acme/test/directory")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var directory map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&directory); err != nil {
		t.Fatal(err)
	}
	if directory["renewalInfo"] != ts.URL+"/acme/test/renewal-info" {
		t.Fatalf("directory 缺少 renewalInfo: %v", directory["renewalInfo"])
	}
	if directory["keyChange"] != ts.URL+"/acme/test/key-change" {
		t.Fatalf("directory 缺少 keyChange: %v", directory["keyChange"])
	}
}
