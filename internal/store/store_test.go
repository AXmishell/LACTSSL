package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lactssl/internal/db"
	"lactssl/internal/model"
	"lactssl/internal/util"
)

func TestCleanupExpiredCertificates(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cipher, err := util.NewCipher([]byte("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	st := New(conn, cipher)

	now := time.Now().UTC()
	expired := &model.Certificate{PrimaryDomain: "expired.example.com", NotBefore: now.AddDate(0, -3, 0), NotAfter: now.Add(-time.Hour)}
	valid := &model.Certificate{PrimaryDomain: "valid.example.com", NotBefore: now.AddDate(0, 0, -1), NotAfter: now.AddDate(0, 1, 0)}
	for _, cert := range []*model.Certificate{expired, valid} {
		if err := st.CreateCertificate(cert); err != nil {
			t.Fatal(err)
		}
	}

	task := &model.Task{Name: "renewal", Domains: []string{"expired.example.com"}}
	if err := st.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateTaskResult(task.ID, "success", "", &now, nil, expired.ID); err != nil {
		t.Fatal(err)
	}

	deleted, err := st.CleanupExpiredCertificates(now)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("应只删除一张过期证书，实际删除 %d 张", deleted)
	}
	if _, err := st.GetCertificate(expired.ID); err == nil {
		t.Fatal("过期证书未被删除")
	}
	if _, err := st.GetCertificate(valid.ID); err != nil {
		t.Fatalf("有效证书不应被删除: %v", err)
	}
	loadedTask, err := st.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loadedTask.CertID != 0 {
		t.Fatalf("任务仍引用已删除证书: %d", loadedTask.CertID)
	}

	deleted, err = st.CleanupAllCertificates()
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("清理全部证书应删除剩余 1 张，实际 %d 张", deleted)
	}
	certs, err := st.ListCertificates()
	if err != nil {
		t.Fatal(err)
	}
	if len(certs) != 0 {
		t.Fatalf("清理后仍存在 %d 张证书", len(certs))
	}
}

func TestCleanupCertificatesFilters(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cipher, err := util.NewCipher([]byte("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	st := New(conn, cipher)

	now := time.Now().UTC()
	revokedAt := now.AddDate(0, 0, -10)
	makeCert := func(domain string, taskID int64, notAfter time.Time, source, ca, status string) *model.Certificate {
		cert := &model.Certificate{
			TaskID:        taskID,
			PrimaryDomain: domain,
			Domains:       []string{domain},
			Source:        source,
			CA:            ca,
			Status:        status,
			NotBefore:     now.AddDate(0, 0, -30),
			NotAfter:      notAfter,
		}
		if err := st.CreateCertificate(cert); err != nil {
			t.Fatal(err)
		}
		return cert
	}
	expiredOld := makeCert("old.example.com", 1, now.AddDate(0, 0, -10), "task", "letsencrypt", "valid")
	expiredRecent := makeCert("recent.example.com", 1, now.AddDate(0, 0, -1), "task", "letsencrypt", "valid")
	oldTaskCert := makeCert("keep.example.com", 2, now.AddDate(0, 0, 5), "task", "letsencrypt", "valid")
	valid := makeCert("valid.example.com", 2, now.AddDate(0, 0, 30), "task", "letsencrypt", "valid")
	revokedOld := makeCert("revoked-old.example.com", 0, now.AddDate(0, 0, 100), "acme", "private:1", "revoked")
	revokedRecent := makeCert("revoked-recent.example.com", 0, now.AddDate(0, 0, 100), "acme", "private:1", "revoked")

	for _, cert := range []*model.Certificate{expiredOld, valid, revokedOld, oldTaskCert} {
		if _, err := conn.Exec(`UPDATE certificates SET created_at=? WHERE id=?`, now.AddDate(0, 0, -10), cert.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conn.Exec(`UPDATE certificates SET revoked_at=? WHERE id IN (?,?)`, revokedAt, revokedOld.ID, revokedRecent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`UPDATE certificates SET revoked_at=? WHERE id=?`, now.AddDate(0, 0, -1), revokedRecent.ID); err != nil {
		t.Fatal(err)
	}

	count := func(filter CertCleanupFilter) int64 {
		t.Helper()
		got, err := st.CountCertificatesForCleanup(filter, now)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := count(CertCleanupFilter{Scope: "expired", ExpiredDays: 5}); got != 1 {
		t.Fatalf("过期天数条件不匹配: %d", got)
	}
	if got := count(CertCleanupFilter{Scope: "revoked", RevokedDays: 5}); got != 1 {
		t.Fatalf("撤销天数条件不匹配: %d", got)
	}
	if got := count(CertCleanupFilter{Scope: "all", Domain: "valid.example"}); got != 1 {
		t.Fatalf("域名条件不匹配: %d", got)
	}
	if got := count(CertCleanupFilter{Scope: "all", CA: "letsencrypt"}); got != 4 {
		t.Fatalf("CA 条件不匹配: %d", got)
	}
	if got := count(CertCleanupFilter{Scope: "all", Source: "acme"}); got != 2 {
		t.Fatalf("来源条件不匹配: %d", got)
	}
	if got := count(CertCleanupFilter{Scope: "all", OlderThanDays: 5}); got != 4 {
		t.Fatalf("签发时间条件不匹配: %d", got)
	}
	if got := count(CertCleanupFilter{Scope: "all", Domain: "keep.example", KeepLatestPerTask: true}); got != 1 {
		t.Fatalf("保留任务最新证书条件不匹配: %d", got)
	}

	deleted, err := st.CleanupCertificates(CertCleanupFilter{Scope: "expired", ExpiredDays: 5}, now)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("按条件清理数量不正确: %d", deleted)
	}
	if _, err := st.GetCertificate(expiredOld.ID); err == nil {
		t.Fatal("符合条件的过期证书未被删除")
	}
	if _, err := st.GetCertificate(expiredRecent.ID); err != nil {
		t.Fatalf("未达到过期天数的证书不应被删除: %v", err)
	}
}

func TestPrivateCAKeyEncryptedAtRest(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cipher, err := util.NewCipher([]byte("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	st := New(conn, cipher)

	ca := &model.PrivateCA{
		Name:       "Internal CA",
		CommonName: "Internal Root CA",
		KeyType:    "ecdsa",
		Source:     "generated",
		Serial:     "01",
		CAType:     "root",
		PathLen:    1,
		CertPEM:    "CERT-PEM",
		KeyPEM:     "PRIVATE-KEY-PEM",
		NotBefore:  time.Now().UTC(),
		NotAfter:   time.Now().UTC().AddDate(1, 0, 0),
	}
	if err := st.CreatePrivateCA(ca); err != nil {
		t.Fatal(err)
	}

	var rawKey string
	if err := conn.QueryRow(`SELECT key_pem FROM private_cas WHERE id=?`, ca.ID).Scan(&rawKey); err != nil {
		t.Fatal(err)
	}
	if rawKey == ca.KeyPEM || !strings.Contains(rawKey, ":") {
		t.Fatalf("私钥未加密落库: %q", rawKey)
	}

	loaded, err := st.GetPrivateCA(ca.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.KeyPEM != ca.KeyPEM {
		t.Fatalf("解密后的私钥不正确: %q", loaded.KeyPEM)
	}

	list, err := st.ListPrivateCAs()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("私有 CA 数量不正确: %d", len(list))
	}
	if list[0].KeyPEM != "" || list[0].CertPEM != "" {
		t.Fatal("列表不应返回证书或私钥内容")
	}
}

// TestCreateFirstUserOnlyOnce 验证初始化只能创建一个管理员。
func TestCreateFirstUserOnlyOnce(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "first-user.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cipher, err := util.NewCipher([]byte("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	st := New(conn, cipher)
	if _, err := st.CreateFirstUser("admin@example.com", "hash"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateFirstUser("second@example.com", "hash"); err == nil {
		t.Fatal("重复初始化应被拒绝")
	}
}

// TestResetPasswordDisablesTOTP 验证密码重置可以解除被会话攻击者绑定的 TOTP。
func TestResetPasswordDisablesTOTP(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "reset.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cipher, err := util.NewCipher([]byte("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	st := New(conn, cipher)
	user, err := st.CreateUser("admin@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetTOTP(user.ID, "TOTP-SECRET", true); err != nil {
		t.Fatal(err)
	}
	if err := st.ResetPassword(user.ID, "new-hash"); err != nil {
		t.Fatal(err)
	}
	updated, err := st.GetUserByID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.TOTPEnabled || updated.TOTPSecret != "" {
		t.Fatalf("密码重置后应关闭 TOTP: enabled=%v secret=%q", updated.TOTPEnabled, updated.TOTPSecret)
	}
}

// TestCertificateKeyEncrypted 验证证书私钥加密落库、读取解密，并支持明文迁移。
func TestCertificateKeyEncrypted(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "cert.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cipher, err := util.NewCipher([]byte("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	st := New(conn, cipher)

	now := time.Now().UTC()
	cert := &model.Certificate{
		PrimaryDomain: "example.com",
		KeyPEM:        "PRIVATE-KEY-PEM",
		CertPEM:       "CERT-PEM",
		NotBefore:     now,
		NotAfter:      now.AddDate(0, 1, 0),
		Status:        "valid",
	}
	if err := st.CreateCertificate(cert); err != nil {
		t.Fatal(err)
	}
	var rawKey string
	if err := conn.QueryRow(`SELECT key_pem FROM certificates WHERE id=?`, cert.ID).Scan(&rawKey); err != nil {
		t.Fatal(err)
	}
	if rawKey == cert.KeyPEM || !strings.Contains(rawKey, "enc:v1:") {
		t.Fatalf("证书私钥未加密落库: %q", rawKey)
	}
	loaded, err := st.GetCertificate(cert.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.KeyPEM != cert.KeyPEM {
		t.Fatalf("解密后的证书私钥不正确: %q", loaded.KeyPEM)
	}

	// 模拟旧版本明文数据并验证迁移。
	if _, err := conn.Exec(`UPDATE certificates SET key_pem=? WHERE id=?`, "LEGACY-KEY", cert.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.MigrateCertificateKeys(); err != nil {
		t.Fatal(err)
	}
	loaded, err = st.GetCertificate(cert.ID)
	if err != nil || loaded.KeyPEM != "LEGACY-KEY" {
		t.Fatalf("迁移后的私钥应可正常读取: %q %v", loaded.KeyPEM, err)
	}
}

// TestTOTPSecretEncrypted 验证 TOTP 密钥加密落库且读取时自动解密。
func TestTOTPSecretEncrypted(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "totp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cipher, err := util.NewCipher([]byte("totp-test-key"))
	if err != nil {
		t.Fatal(err)
	}
	st := New(conn, cipher)
	user, err := st.CreateUser("totp@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	secret := "JBSWY3DPEHPK3PXP"
	if err := st.SetTOTP(user.ID, secret, true); err != nil {
		t.Fatal(err)
	}

	var stored string
	if err := conn.QueryRow(`SELECT totp_secret FROM users WHERE id=?`, user.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == secret || !strings.HasPrefix(stored, "enc:v1:") {
		t.Fatalf("TOTP 密钥未加密落库: %q", stored)
	}

	loaded, err := st.GetUserByID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TOTPSecret != secret || !loaded.TOTPEnabled {
		t.Fatalf("读取 TOTP 密钥不正确: %+v", loaded)
	}
}

// TestMigrateTOTPSecrets 验证旧版本明文 TOTP 密钥会在启动时加密。
func TestMigrateTOTPSecrets(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "totp-migrate.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cipher, err := util.NewCipher([]byte("totp-test-key"))
	if err != nil {
		t.Fatal(err)
	}
	st := New(conn, cipher)
	user, err := st.CreateUser("legacy@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`UPDATE users SET totp_secret='LEGACYSECRET' WHERE id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.MigrateTOTPSecrets(); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := conn.QueryRow(`SELECT totp_secret FROM users WHERE id=?`, user.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !util.IsEncrypted(stored) {
		t.Fatalf("旧 TOTP 密钥未被加密: %q", stored)
	}
	loaded, err := st.GetUserByID(user.ID)
	if err != nil || loaded.TOTPSecret != "LEGACYSECRET" {
		t.Fatalf("迁移后无法解密: %+v, %v", loaded, err)
	}
}
