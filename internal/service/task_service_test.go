package service

import (
	"encoding/json"
	"testing"

	"lactssl/internal/model"
)

func TestTaskInputDoesNotBindPreservedDNSRoutes(t *testing.T) {
	var in TaskInput
	raw := `{"domains":["example.com"],"preserved_dns_routes":[{"dns_account_id":1}],"PreservedDNSRoutes":[{"dns_account_id":2}]}`
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatal(err)
	}
	if len(in.PreservedDNSRoutes) != 0 {
		t.Fatalf("客户端不能注入 PreservedDNSRoutes: %#v", in.PreservedDNSRoutes)
	}
}

func TestRequiresDeployExecutionFor(t *testing.T) {
	if RequiresDeployExecutionFor(model.FlowStandard, nil, model.TargetFlowConfig{}) {
		t.Fatal("没有部署节点时不应要求部署权限")
	}
	nodes := []model.PipelineNode{{Type: model.PipelineDeploy, Enabled: true}}
	if !RequiresDeployExecutionFor(model.FlowStandard, nodes, model.TargetFlowConfig{}) {
		t.Fatal("启用部署节点时应要求部署权限")
	}
	if !RequiresDeployExecutionFor(model.FlowTargetBeta, nil, model.TargetFlowConfig{}) {
		t.Fatal("目标流程总会执行部署阶段")
	}
}
