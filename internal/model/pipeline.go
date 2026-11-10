package model

import "fmt"

// 任务流程节点类型。任务按数组顺序依次执行这些模块。
const (
	PipelineApply  = "apply"
	PipelineDeploy = "deploy"
	PipelineNotify = "notify"
)

// 通知触发时机。
const (
	NotifyAllChannels = "all"
	NotifyAlways      = "always"
	NotifySuccess     = "success"
	NotifyFailure     = "failure"
)

// PipelineConfig 是任务流程节点的配置。不同节点只会使用其中一部分字段。
type PipelineConfig struct {
	// 申请证书节点
	Domains      []string `json:"domains,omitempty"`
	DNSAccountID int64    `json:"dns_account_id,omitempty"`
	CA           string   `json:"ca,omitempty"`
	KeyType      string   `json:"key_type,omitempty"`
	AutoRenew    bool     `json:"auto_renew,omitempty"`
	RenewDays    int      `json:"renew_days,omitempty"`

	// 部署证书节点
	DeployAccountID int64             `json:"deploy_account_id,omitempty"`
	DeployConfig    map[string]string `json:"deploy_config,omitempty"`

	// 通知节点
	NotifyChannel  string   `json:"notify_channel,omitempty"`  // 兼容字段
	NotifyChannels []string `json:"notify_channels,omitempty"` // 选择的已启用渠道，为空表示全部
	NotifyOn       string   `json:"notify_on,omitempty"`       // always / success / failure
	Subject        string   `json:"subject,omitempty"`
}

// PipelineNode 是任务流程中的一个模块。
type PipelineNode struct {
	ID      string         `json:"id"`
	Type    string         `json:"type"`
	Name    string         `json:"name"`
	Enabled bool           `json:"enabled"`
	Config  PipelineConfig `json:"config"`
}

// EffectivePipeline 返回任务实际执行的任务流程。
// 旧任务没有保存任务流程时，根据原有字段推导出「申请 -> 部署... -> 通知」。
func (t *Task) EffectivePipeline() []PipelineNode {
	if len(t.Pipeline) > 0 {
		out := make([]PipelineNode, len(t.Pipeline))
		copy(out, t.Pipeline)
		return out
	}

	nodes := []PipelineNode{{
		ID:      "apply",
		Type:    PipelineApply,
		Enabled: true,
		Config: PipelineConfig{
			Domains:      append([]string(nil), t.Domains...),
			DNSAccountID: t.DNSAccountID,
			CA:           t.CA,
			KeyType:      t.KeyType,
			AutoRenew:    t.AutoRenew,
			RenewDays:    t.RenewDays,
		},
	}}
	for i, id := range t.DeployIDs {
		nodes = append(nodes, PipelineNode{
			ID:      fmt.Sprintf("deploy-%d", i+1),
			Type:    PipelineDeploy,
			Enabled: true,
			Config:  PipelineConfig{DeployAccountID: id},
		})
	}
	nodes = append(nodes, PipelineNode{
		ID:      "notify",
		Type:    PipelineNotify,
		Enabled: true,
		Config:  PipelineConfig{NotifyChannel: NotifyAllChannels, NotifyOn: NotifyAlways},
	})
	return nodes
}

// HasPipeline 表示任务是否使用了显式配置的任务流程。
func (t *Task) HasPipeline() bool { return len(t.Pipeline) > 0 }

// PipelineDeployIDs 从任务流程中提取目标凭据 ID。
func (t *Task) PipelineDeployIDs() []int64 {
	ids := make([]int64, 0)
	for _, node := range t.EffectivePipeline() {
		if node.Type == PipelineDeploy && node.Enabled && node.Config.DeployAccountID > 0 {
			ids = append(ids, node.Config.DeployAccountID)
		}
	}
	return ids
}
