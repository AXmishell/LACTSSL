package api

import (
	"path/filepath"
	"testing"
	"time"

	"lactssl/internal/db"
	"lactssl/internal/model"
	"lactssl/internal/service"
	"lactssl/internal/store"
	"lactssl/internal/util"
)

func newPipelineTestServer(t *testing.T) *Server {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	cipher, err := util.NewCipher([]byte("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(conn, cipher)
	if err := st.CreateDNSAccount(&model.DNSAccount{Name: "dns", Provider: "cloudflare", Config: map[string]string{"api_token": "x"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDeployAccount(&model.DeployAccount{Name: "local", Type: "local", Config: map[string]string{"cert_path": "/tmp/cert", "key_path": "/tmp/key"}}); err != nil {
		t.Fatal(err)
	}
	return &Server{Store: st}
}

func TestFillTaskBodyNormalizesPipeline(t *testing.T) {
	s := newPipelineTestServer(t)
	dnsAccounts, err := s.Store.ListDNSAccounts()
	if err != nil || len(dnsAccounts) == 0 {
		t.Fatalf("缺少 DNS 账户: %v", err)
	}
	deployAccounts, err := s.Store.ListDeployAccounts()
	if err != nil || len(deployAccounts) == 0 {
		t.Fatalf("缺少目标凭据: %v", err)
	}

	body := &taskBody{
		Domains:      []string{"example.com", "www.example.com"},
		DNSAccountID: dnsAccounts[0].ID,
		CA:           "letsencrypt",
		KeyType:      "ecdsa",
		AutoRenew:    true,
		RenewDays:    30,
		Pipeline: []model.PipelineNode{
			{ID: "apply", Type: model.PipelineApply, Name: "申请证书", Enabled: true},
			{ID: "deploy-1", Type: model.PipelineDeploy, Enabled: true, Config: model.PipelineConfig{
				DeployAccountID: deployAccounts[0].ID,
				DeployConfig:    map[string]string{"cert_path": "/tmp/cert", "key_path": "/tmp/key"},
			}},
			{ID: "notify-1", Type: model.PipelineNotify, Enabled: true, Config: model.PipelineConfig{}},
		},
	}
	if err := s.fillTaskBody(body); err != nil {
		t.Fatalf("任务配置校验失败: %v", err)
	}
	if len(body.Pipeline) != 3 {
		t.Fatalf("任务流程节点数量不正确: %d", len(body.Pipeline))
	}
	if body.Pipeline[0].Config.DNSAccountID != dnsAccounts[0].ID {
		t.Fatalf("申请节点未同步 DNS 账户: %+v", body.Pipeline[0])
	}
	if body.Pipeline[2].Config.NotifyChannel != model.NotifyAllChannels || body.Pipeline[2].Config.NotifyOn != model.NotifyAlways {
		t.Fatalf("通知节点默认值不正确: %+v", body.Pipeline[2])
	}
	if len(body.DeployIDs) != 1 || body.DeployIDs[0] != deployAccounts[0].ID {
		t.Fatalf("部署 ID 未从任务流程同步: %v", body.DeployIDs)
	}
	if body.Pipeline[1].Config.DeployConfig["cert_path"] != "/tmp/cert" {
		t.Fatalf("部署参数未保留: %+v", body.Pipeline[1].Config.DeployConfig)
	}
}

func TestFillTaskBodyRejectsMissingDeployConfig(t *testing.T) {
	s := newPipelineTestServer(t)
	dnsAccounts, _ := s.Store.ListDNSAccounts()
	account := &model.DeployAccount{Name: "empty-local", Type: "local", Config: map[string]string{}}
	if err := s.Store.CreateDeployAccount(account); err != nil {
		t.Fatal(err)
	}
	body := &taskBody{
		Domains:      []string{"example.com"},
		DNSAccountID: dnsAccounts[0].ID,
		CA:           "letsencrypt",
		KeyType:      "ecdsa",
		RenewDays:    30,
		Pipeline: []model.PipelineNode{
			{Type: model.PipelineApply},
			{Type: model.PipelineDeploy, Enabled: true, Config: model.PipelineConfig{DeployAccountID: account.ID}},
		},
	}
	if err := s.fillTaskBody(body); err == nil {
		t.Fatal("启用部署节点缺少路径时应被拒绝")
	}
}

func TestFillTaskBodyRejectsDuplicateApplyNode(t *testing.T) {
	s := newPipelineTestServer(t)
	dnsAccounts, _ := s.Store.ListDNSAccounts()
	body := &taskBody{
		Domains:      []string{"example.com"},
		DNSAccountID: dnsAccounts[0].ID,
		CA:           "letsencrypt",
		KeyType:      "ecdsa",
		RenewDays:    30,
		Pipeline: []model.PipelineNode{
			{Type: model.PipelineApply},
			{Type: model.PipelineApply},
		},
	}
	if err := s.fillTaskBody(body); err == nil {
		t.Fatal("重复的申请证书节点应当被拒绝")
	}
}

func TestFillTaskBodyDisabledDeployWithoutTarget(t *testing.T) {
	s := newPipelineTestServer(t)
	dnsAccounts, _ := s.Store.ListDNSAccounts()
	body := &taskBody{
		Domains:      []string{"example.com"},
		DNSAccountID: dnsAccounts[0].ID,
		CA:           "letsencrypt",
		KeyType:      "ecdsa",
		RenewDays:    30,
		Pipeline: []model.PipelineNode{
			{Type: model.PipelineApply},
			{Type: model.PipelineDeploy, Enabled: false},
		},
	}
	if err := s.fillTaskBody(body); err != nil {
		t.Fatalf("停用的部署节点不应要求目标凭据: %v", err)
	}
	if len(body.DeployIDs) != 0 {
		t.Fatalf("停用的部署节点不应写入 deploy_ids: %v", body.DeployIDs)
	}
}

func TestFillTaskBodyAllowsPrivateCAWithoutDNS(t *testing.T) {
	s := newPipelineTestServer(t)
	ca := &model.PrivateCA{
		Name:       "Internal CA",
		CommonName: "Internal CA",
		KeyType:    "ecdsa",
		Source:     "generated",
		CAType:     "root",
		PathLen:    1,
		NotBefore:  time.Now().UTC(),
		NotAfter:   time.Now().UTC().AddDate(1, 0, 0),
	}
	if err := s.Store.CreatePrivateCA(ca); err != nil {
		t.Fatal(err)
	}
	body := &taskBody{
		Domains:   []string{"internal.example.com"},
		CA:        service.PrivateCAValue(ca.ID),
		KeyType:   "ecdsa",
		RenewDays: 30,
		Pipeline:  []model.PipelineNode{{Type: model.PipelineApply}},
	}
	if err := s.fillTaskBody(body); err != nil {
		t.Fatalf("私有 CA 任务不应要求 DNS 账户: %v", err)
	}
	if body.Pipeline[0].Config.CA != service.PrivateCAValue(ca.ID) {
		t.Fatalf("申请节点未同步私有 CA: %+v", body.Pipeline[0])
	}
	if body.Pipeline[0].Config.DNSAccountID != 0 {
		t.Fatalf("私有 CA 任务不应写入 DNS 账户: %d", body.Pipeline[0].Config.DNSAccountID)
	}
}

func TestFillTaskBodyRejectsExpiredPrivateCA(t *testing.T) {
	s := newPipelineTestServer(t)
	ca := &model.PrivateCA{
		Name:       "Expired CA",
		CommonName: "Expired CA",
		KeyType:    "ecdsa",
		Source:     "generated",
		CAType:     "root",
		PathLen:    1,
		NotBefore:  time.Now().UTC().AddDate(-1, 0, 0),
		NotAfter:   time.Now().UTC().AddDate(0, 0, -1),
	}
	if err := s.Store.CreatePrivateCA(ca); err != nil {
		t.Fatal(err)
	}
	body := &taskBody{
		Domains:   []string{"internal.example.com"},
		CA:        service.PrivateCAValue(ca.ID),
		KeyType:   "ecdsa",
		RenewDays: 30,
	}
	if err := s.fillTaskBody(body); err == nil {
		t.Fatal("已过期的私有 CA 应被拒绝")
	}
}
