package model

import "testing"

func TestEffectivePipelineLegacyTask(t *testing.T) {
	task := &Task{
		Domains:      []string{"example.com", "www.example.com"},
		DNSAccountID: 7,
		CA:           "letsencrypt",
		KeyType:      "ecdsa",
		AutoRenew:    true,
		RenewDays:    30,
		DeployIDs:    []int64{3, 5},
	}

	pipeline := task.EffectivePipeline()
	if len(pipeline) != 4 {
		t.Fatalf("旧任务的任务流程节点数量不正确: %d", len(pipeline))
	}
	if pipeline[0].Type != PipelineApply || pipeline[0].Config.DNSAccountID != 7 {
		t.Fatalf("申请节点不正确: %+v", pipeline[0])
	}
	if pipeline[1].Type != PipelineDeploy || pipeline[1].Config.DeployAccountID != 3 {
		t.Fatalf("第一个部署节点不正确: %+v", pipeline[1])
	}
	if pipeline[2].Config.DeployAccountID != 5 {
		t.Fatalf("第二个部署节点不正确: %+v", pipeline[2])
	}
	if pipeline[3].Type != PipelineNotify || pipeline[3].Config.NotifyOn != NotifyAlways {
		t.Fatalf("通知节点不正确: %+v", pipeline[3])
	}
	if task.HasPipeline() {
		t.Fatal("旧任务不应被标记为显式任务流程")
	}
}

func TestEffectivePipelineUsesExplicitNodes(t *testing.T) {
	task := &Task{
		DeployIDs: []int64{9},
		Pipeline: []PipelineNode{
			{ID: "apply", Type: PipelineApply, Enabled: true},
			{ID: "deploy-1", Type: PipelineDeploy, Enabled: false, Config: PipelineConfig{DeployAccountID: 9}},
		},
	}
	pipeline := task.EffectivePipeline()
	if len(pipeline) != 2 {
		t.Fatalf("显式任务流程节点数量不正确: %d", len(pipeline))
	}
	if ids := task.PipelineDeployIDs(); len(ids) != 0 {
		t.Fatalf("停用的部署节点不应参与执行: %v", ids)
	}
}
