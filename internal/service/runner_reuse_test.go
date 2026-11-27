package service

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
)

func makeReusableCertificate(t *testing.T, domains []string, notAfter time.Time) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: domains[0]},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		DNSNames:     domains,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return string(certPEM), string(certcrypto.PEMEncode(key))
}

func reusableCertificateRecord(t *testing.T, taskID int64, domains []string, ca string, notAfter time.Time) *model.Certificate {
	t.Helper()
	certPEM, keyPEM := makeReusableCertificate(t, domains, notAfter)
	return &model.Certificate{
		ID:            1,
		TaskID:        taskID,
		Domains:       domains,
		PrimaryDomain: domains[0],
		Issuer:        "Test CA",
		CA:            ca,
		CertPEM:       certPEM,
		KeyPEM:        keyPEM,
		FullchainPEM:  certPEM,
		NotBefore:     time.Now().Add(-time.Hour),
		NotAfter:      notAfter,
		Status:        "valid",
	}
}

func TestCertificateReusable(t *testing.T) {
	domains := []string{"*.example.com"}
	valid := reusableCertificateRecord(t, 1, domains, "letsencrypt", time.Now().Add(60*24*time.Hour))
	if !certificateReusable(valid, 30, "letsencrypt", map[string]bool{"letsencrypt": true}, "ecdsa", domains) {
		t.Fatal("有效证书应当可以复用")
	}

	tests := []struct {
		name     string
		mutate   func(*model.Certificate)
		ca       string
		keyType  string
		domains  []string
		reusable bool
	}{
		{name: "进入续期窗口", mutate: func(c *model.Certificate) {
			c.NotBefore = time.Now().Add(-80 * 24 * time.Hour)
			c.NotAfter = time.Now().Add(10 * 24 * time.Hour)
		}},
		{name: "已过期", mutate: func(c *model.Certificate) { c.NotAfter = time.Now().Add(-time.Hour) }},
		{name: "已撤销", mutate: func(c *model.Certificate) { now := time.Now(); c.RevokedAt = &now }},
		{name: "状态异常", mutate: func(c *model.Certificate) { c.Status = "revoked" }},
		{name: "CA 不同", ca: "other-ca"},
		{name: "私钥类型不同", keyType: "rsa2048"},
		{name: "域名集合不同", domains: []string{"*.example.com", "example.com"}},
		{name: "缺少私钥", mutate: func(c *model.Certificate) { c.KeyPEM = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cert := reusableCertificateRecord(t, 1, domains, "letsencrypt", time.Now().Add(60*24*time.Hour))
			if test.mutate != nil {
				test.mutate(cert)
			}
			ca := "letsencrypt"
			if test.ca != "" {
				ca = test.ca
			}
			keyType := "ecdsa"
			if test.keyType != "" {
				keyType = test.keyType
			}
			wantDomains := domains
			if test.domains != nil {
				wantDomains = test.domains
			}
			if certificateReusable(cert, 30, ca, map[string]bool{ca: true}, keyType, wantDomains) {
				t.Fatal("不应复用该证书")
			}
		})
	}
}

func TestRunApplyNodeReusesValidCertificate(t *testing.T) {
	m := newTestManager(t)
	domains := []string{"*.example.com"}
	task := &model.Task{
		Name:      "reuse",
		Domains:   domains,
		CA:        "letsencrypt",
		KeyType:   "ecdsa",
		AutoRenew: true,
		RenewDays: 30,
		Enabled:   true,
	}
	if err := m.Store.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	cert := reusableCertificateRecord(t, task.ID, domains, "letsencrypt", time.Now().Add(60*24*time.Hour))
	if err := m.Store.CreateCertificate(cert); err != nil {
		t.Fatal(err)
	}
	node := model.PipelineNode{
		Type:    model.PipelineApply,
		Enabled: true,
		Config:  model.PipelineConfig{Domains: domains, CA: "letsencrypt", KeyType: "ecdsa"},
	}

	got, issued, err := m.runApplyNode(task, node, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if issued {
		t.Fatal("有效证书不应触发新签发")
	}
	if got.ID != cert.ID {
		t.Fatalf("复用了错误的证书: want %d, got %d", cert.ID, got.ID)
	}
	certs, err := m.Store.ListCertificates()
	if err != nil {
		t.Fatal(err)
	}
	if len(certs) != 1 {
		t.Fatalf("复用时不应新增证书记录，实际 %d 条", len(certs))
	}

	// 另一个相同域名集合的任务也应复用同一张证书，避免重复任务反复消耗 CA 配额。
	duplicate := &model.Task{
		Name:      "duplicate",
		Domains:   domains,
		CA:        "letsencrypt",
		KeyType:   "ecdsa",
		AutoRenew: true,
		RenewDays: 30,
		Enabled:   true,
	}
	if err := m.Store.CreateTask(duplicate); err != nil {
		t.Fatal(err)
	}
	got, issued, err = m.runApplyNode(duplicate, node, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if issued || got.ID != cert.ID {
		t.Fatalf("重复任务应复用已有证书: issued=%v cert=%d", issued, got.ID)
	}
}

func TestRunApplyNodeForceIssueBypassesReuse(t *testing.T) {
	m := newTestManager(t)
	domains := []string{"force.example.com"}
	task := &model.Task{
		Name:      "force",
		Domains:   domains,
		CA:        "letsencrypt",
		KeyType:   "ecdsa",
		AutoRenew: true,
		RenewDays: 30,
		Enabled:   true,
	}
	if err := m.Store.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	cert := reusableCertificateRecord(t, task.ID, domains, "letsencrypt", time.Now().Add(60*24*time.Hour))
	if err := m.Store.CreateCertificate(cert); err != nil {
		t.Fatal(err)
	}
	node := model.PipelineNode{
		Type:    model.PipelineApply,
		Enabled: true,
		Config:  model.PipelineConfig{Domains: domains, CA: "letsencrypt", KeyType: "ecdsa"},
	}
	_, issued, err := m.runApplyNode(task, node, RunOptions{ForceIssue: true})
	if err == nil {
		t.Fatal("强制签发应跳过复用并进入签发流程，缺少 ACME 邮箱时应返回错误")
	}
	if issued {
		t.Fatal("签发失败时不应标记为已签发")
	}
}

func TestRunApplyNodeDoesNotReuseDueCertificate(t *testing.T) {
	m := newTestManager(t)
	domains := []string{"due.example.com"}
	task := &model.Task{
		Name:      "due",
		Domains:   domains,
		CA:        "letsencrypt",
		KeyType:   "ecdsa",
		AutoRenew: true,
		RenewDays: 30,
		Enabled:   true,
	}
	if err := m.Store.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	cert := reusableCertificateRecord(t, task.ID, domains, "letsencrypt", time.Now().Add(24*time.Hour))
	cert.NotBefore = time.Now().Add(-9 * 24 * time.Hour)
	if err := m.Store.CreateCertificate(cert); err != nil {
		t.Fatal(err)
	}
	node := model.PipelineNode{
		Type:    model.PipelineApply,
		Enabled: true,
		Config:  model.PipelineConfig{Domains: domains, CA: "letsencrypt", KeyType: "ecdsa"},
	}
	_, issued, err := m.runApplyNode(task, node, RunOptions{})
	if err == nil {
		t.Fatal("进入续期窗口后不应复用证书，缺少 ACME 邮箱时应返回错误")
	}
	if issued {
		t.Fatal("签发失败时不应标记为已签发")
	}
	if !strings.Contains(i18n.ErrorText(i18n.ZhCN, err), "邮箱") {
		t.Fatalf("应进入签发流程并提示缺少邮箱，实际错误: %v", err)
	}
}

func TestNextRenewTimeForUsesOriginalLifetime(t *testing.T) {
	now := time.Now().UTC()
	notBefore := now.Add(-92 * time.Hour) // 6 天寿命已过去 92 小时
	notAfter := notBefore.Add(6 * 24 * time.Hour)
	next := NextRenewTimeFor(notBefore, notAfter, 30, true)
	if next == nil {
		t.Fatal("自动续期应返回下次执行时间")
	}
	// 短寿命证书在寿命的 2/3 处续期，即签发后 96 小时。
	want := notBefore.Add(96 * time.Hour)
	if next.Sub(want) > time.Minute || want.Sub(*next) > time.Minute {
		t.Fatalf("应按原始寿命计算续期时间: want %v, got %v", want, *next)
	}
}

func TestHandleRateLimited(t *testing.T) {
	m := newTestManager(t)
	domains := []string{"*.example.com"}
	task := &model.Task{
		Name:      "limited",
		Domains:   domains,
		CA:        "letsencrypt",
		KeyType:   "ecdsa",
		AutoRenew: true,
		RenewDays: 30,
		Enabled:   true,
	}
	if err := m.Store.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	cert := reusableCertificateRecord(t, task.ID, domains, "letsencrypt", time.Now().Add(60*24*time.Hour))
	if err := m.Store.CreateCertificate(cert); err != nil {
		t.Fatal(err)
	}

	start := time.Now().UTC()
	retryAt := time.Now().UTC().Add(34 * time.Hour).Truncate(time.Second)
	if err := m.handleRateLimited(task, nil, &RateLimitError{RetryAt: retryAt, Detail: "rate limited"}, &start); err != nil {
		t.Fatal(err)
	}
	loaded, err := m.Store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != "rate_limited" {
		t.Fatalf("任务状态应为 rate_limited，实际 %s", loaded.Status)
	}
	if loaded.NextRunAt == nil {
		t.Fatal("自动续期任务应安排恢复后的重试时间")
	}
	wantRetry := retryAt.Add(time.Minute)
	if diff := loaded.NextRunAt.Sub(wantRetry); diff < -time.Minute || diff > time.Minute {
		t.Fatalf("重试时间不正确: want %v, got %v", wantRetry, loaded.NextRunAt)
	}
	key, params, ok := i18n.Decode(loaded.Message)
	if !ok || key != "task.message.rate_limited_existing" {
		t.Fatalf("限流消息应说明现有证书仍有效: key=%s params=%v", key, params)
	}
	if _, exists := params["expires"]; !exists {
		t.Fatalf("限流消息缺少现有证书到期时间: %v", params)
	}
}

func TestHandleRateLimitedWithoutAutoRenew(t *testing.T) {
	m := newTestManager(t)
	domains := []string{"example.com"}
	task := &model.Task{
		Name:      "limited-manual",
		Domains:   domains,
		CA:        "letsencrypt",
		KeyType:   "ecdsa",
		AutoRenew: false,
		RenewDays: 30,
		Enabled:   true,
	}
	if err := m.Store.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	cert := reusableCertificateRecord(t, task.ID, domains, "letsencrypt", time.Now().Add(60*24*time.Hour))
	if err := m.Store.CreateCertificate(cert); err != nil {
		t.Fatal(err)
	}

	start := time.Now().UTC()
	retryAt := time.Now().UTC().Add(34 * time.Hour)
	if err := m.handleRateLimited(task, nil, &RateLimitError{RetryAt: retryAt, Detail: "rate limited"}, &start); err != nil {
		t.Fatal(err)
	}
	loaded, err := m.Store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != "failed" {
		t.Fatalf("未启用自动续期时不应保持限流等待状态，实际 %s", loaded.Status)
	}
	if loaded.NextRunAt != nil {
		t.Fatalf("未启用自动续期时不应安排重试: %v", loaded.NextRunAt)
	}
	key, params, ok := i18n.Decode(loaded.Message)
	if !ok || key != "task.message.rate_limited_no_retry_existing" {
		t.Fatalf("限流消息应说明未安排自动重试: key=%s params=%v", key, params)
	}
	if _, exists := params["expires"]; !exists {
		t.Fatalf("限流消息缺少现有证书到期时间: %v", params)
	}
}
