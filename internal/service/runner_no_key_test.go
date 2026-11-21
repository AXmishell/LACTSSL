package service

import (
	"strings"
	"testing"

	"lactssl/internal/model"
)

// TestRunApplyNodeWithoutKeyStorage 验证勾选「不存储证书私钥」后，
// 私钥不会写入数据库，但本次执行仍可在内存中用于后续部署。
func TestRunApplyNodeWithoutKeyStorage(t *testing.T) {
	m := newTestManager(t)
	ca, err := GeneratePrivateCA(PrivateCARequest{
		Name:         "No-Key Test CA",
		CommonName:   "No-Key Test CA",
		Organization: "LACTSSL",
		Country:      "CN",
		KeyType:      "ecdsa",
		ValidDays:    365,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Store.CreatePrivateCA(ca); err != nil {
		t.Fatal(err)
	}

	task := &model.Task{
		Name:         "no-key",
		Domains:      []string{"no-key.example.com"},
		CA:           PrivateCAValue(ca.ID),
		KeyType:      "ecdsa",
		NoKeyStorage: true,
		AutoRenew:    true,
		RenewDays:    30,
		Enabled:      true,
	}
	if err := m.Store.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	storedTask, err := m.Store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !storedTask.NoKeyStorage {
		t.Fatal("任务的不存储私钥配置未持久化")
	}

	node := model.PipelineNode{
		Type:    model.PipelineApply,
		Enabled: true,
		Config: model.PipelineConfig{
			Domains:      task.Domains,
			CA:           task.CA,
			KeyType:      task.KeyType,
			NoKeyStorage: true,
		},
	}
	got, issued, err := m.runApplyNode(task, node, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !issued {
		t.Fatal("不存储私钥时应重新签发证书")
	}
	if strings.TrimSpace(got.KeyPEM) == "" {
		t.Fatal("本次执行应保留内存中的私钥以供部署使用")
	}

	stored, err := m.Store.GetCertificate(got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stored.KeyPEM) != "" {
		t.Fatal("证书私钥不应写入数据库")
	}
	if stored.KeyAvailable {
		t.Fatal("未保存私钥的证书不应标记为可下载私钥")
	}

	// 再次执行时不能复用没有私钥的历史证书，否则部署将缺少私钥。
	second, issued, err := m.runApplyNode(task, node, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !issued {
		t.Fatal("不存储私钥的任务应重新签发而不是复用")
	}
	if second.ID == got.ID {
		t.Fatal("重新签发应生成新的证书记录")
	}
}
