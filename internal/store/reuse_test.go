package store

import (
	"testing"
	"time"

	"lactssl/internal/model"
)

func TestLatestTaskCertificateAndListReusable(t *testing.T) {
	st := newTestStore(t)
	now := time.Now().UTC()
	newCert := func(taskID int64, ca, status string, notAfter time.Time, key string) *model.Certificate {
		return &model.Certificate{
			TaskID:        taskID,
			Domains:       []string{"example.com"},
			PrimaryDomain: "example.com",
			CA:            ca,
			CertPEM:       "cert",
			KeyPEM:        key,
			NotBefore:     now.Add(-time.Hour),
			NotAfter:      notAfter,
			Status:        status,
		}
	}

	expired := newCert(1, "ca-a", "valid", now.Add(-time.Hour), "key")
	otherCA := newCert(1, "ca-b", "valid", now.Add(24*time.Hour), "key")
	noKey := newCert(1, "ca-a", "valid", now.Add(24*time.Hour), "")
	valid := newCert(1, "ca-a", "valid", now.Add(24*time.Hour), "key")
	for _, cert := range []*model.Certificate{expired, otherCA, noKey, valid} {
		if err := st.CreateCertificate(cert); err != nil {
			t.Fatal(err)
		}
	}

	list, err := st.ListReusableCertificates("ca-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != valid.ID {
		t.Fatalf("应只返回有效且包含私钥的证书: %+v", list)
	}

	latest, err := st.LatestTaskCertificate(1)
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.ID != valid.ID {
		t.Fatalf("应返回任务最近一张证书: %+v", latest)
	}

	missing, err := st.LatestTaskCertificate(99)
	if err != nil {
		t.Fatal(err)
	}
	if missing != nil {
		t.Fatalf("不存在的任务应返回 nil: %+v", missing)
	}
}
