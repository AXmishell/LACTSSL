package store

import (
	"testing"
	"time"

	"lactssl/internal/model"
)

func newTestCSRRequest(publicID string) *model.CSRRequest {
	expires := time.Now().UTC().Add(time.Hour)
	return &model.CSRRequest{
		PublicID:      publicID,
		CSRPEM:        "-----BEGIN CERTIFICATE REQUEST-----\nMIIB\n-----END CERTIFICATE REQUEST-----\n",
		CSRHash:       "hash-" + publicID,
		Identifiers:   []string{"example.com"},
		PrimaryDomain: "example.com",
		CA:            "letsencrypt",
		Challenge:     "dns01",
		DNSAccountID:  1,
		Status:        model.CSRStatusPending,
		ExpiresAt:     &expires,
	}
}

func TestCSRRequestLifecycle(t *testing.T) {
	st := newTestStore(t)
	req := newTestCSRRequest("csr-one")
	if err := st.CreateCSRRequest(req); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.GetCSRRequestByPublicID("csr-one")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != req.ID || loaded.Status != model.CSRStatusPending {
		t.Fatalf("申请读取不正确: %+v", loaded)
	}
	if len(loaded.Identifiers) != 1 || loaded.Identifiers[0] != "example.com" {
		t.Fatalf("标识列表不正确: %v", loaded.Identifiers)
	}

	claimed, err := st.ClaimCSRRequest(req.ID)
	if err != nil || !claimed {
		t.Fatalf("申请应能被认领: %v %v", claimed, err)
	}
	if again, _ := st.ClaimCSRRequest(req.ID); again {
		t.Fatal("同一申请不应被重复认领")
	}

	cert := &model.Certificate{
		Domains:       []string{"example.com"},
		PrimaryDomain: "example.com",
		CA:            "letsencrypt",
		Source:        "csr",
		CertPEM:       "cert",
		FullchainPEM:  "fullchain",
		Status:        "valid",
		NotBefore:     time.Now().UTC(),
		NotAfter:      time.Now().UTC().Add(90 * 24 * time.Hour),
	}
	if err := st.CompleteCSRRequest(req.ID, cert); err != nil {
		t.Fatal(err)
	}
	loaded, err = st.GetCSRRequest(req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != model.CSRStatusIssued || loaded.CertificateID != cert.ID {
		t.Fatalf("申请未正确标记为已签发: %+v", loaded)
	}
	saved, err := st.GetCertificate(cert.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.CSRRequestID != req.ID || saved.KeyAvailable {
		t.Fatalf("证书来源或私钥状态不正确: %+v", saved)
	}
}

func TestCSRRequestRetryAndUniqueness(t *testing.T) {
	st := newTestStore(t)
	req := newTestCSRRequest("csr-retry")
	if err := st.CreateCSRRequest(req); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimCSRRequest(req.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.SetCSRRequestFailed(req.ID, "csr.issue_failed", "temporary", true); err != nil {
		t.Fatal(err)
	}
	ok, err := st.RetryCSRRequest(req.ID)
	if err != nil || !ok {
		t.Fatalf("可重试申请应重新排队: %v %v", ok, err)
	}
	loaded, _ := st.GetCSRRequest(req.ID)
	if loaded.Status != model.CSRStatusPending || loaded.Attempts != 1 {
		t.Fatalf("重试状态不正确: %+v", loaded)
	}

	// 同一 CSR 与 CA 只允许一个在途申请。
	duplicate := newTestCSRRequest("csr-duplicate")
	duplicate.CSRHash = req.CSRHash
	if err := st.CreateCSRRequest(duplicate); err == nil {
		t.Fatal("重复的在途 CSR 应触发唯一约束")
	}
	if active, err := st.GetActiveCSRRequestByHash(req.CSRHash, req.CA, 0); err != nil || active.ID != req.ID {
		t.Fatalf("应返回已有在途申请: %+v %v", active, err)
	}
}

func TestCSRRequestCancel(t *testing.T) {
	st := newTestStore(t)
	req := newTestCSRRequest("csr-cancel")
	if err := st.CreateCSRRequest(req); err != nil {
		t.Fatal(err)
	}
	ok, err := st.SetCSRRequestCancelled(req.ID)
	if err != nil || !ok {
		t.Fatalf("等待中的申请应能取消: %v %v", ok, err)
	}
	if again, _ := st.SetCSRRequestCancelled(req.ID); again {
		t.Fatal("已取消申请不应再次取消")
	}
	if _, err := st.ClaimCSRRequest(req.ID); err != nil {
		t.Fatal(err)
	}
}
