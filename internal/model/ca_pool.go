package model

import "time"

// CA 调度组策略。
const (
	// CAPoolStrategyRoundRobin 按序号轮询：每个调度组维护独立游标，
	// 每次签发从下一个候选开始，失败时按环形顺序继续尝试。
	CAPoolStrategyRoundRobin = "round_robin"
	// CAPoolStrategyRandom 随机抽取：每次签发打乱候选顺序后依次尝试。
	CAPoolStrategyRandom = "random"
	// CAPoolStrategyPriority 固定优先级：始终按 priority 从小到大尝试。
	CAPoolStrategyPriority = "priority"
)

// CAPool 是一组用于证书申请的候选 CA。
// 一个系统可以配置多个调度组，不同任务可以选择不同策略。
type CAPool struct {
	ID       int64  `json:"id"`
	PublicID string `json:"public_id"`
	Name     string `json:"name"`
	// Strategy 为 round_robin、random 或 priority。
	Strategy string `json:"strategy"`
	// Cursor 只用于轮询策略，保存已分配的起始序号，不直接暴露给前端。
	Cursor    int64        `json:"-"`
	Enabled   bool         `json:"enabled"`
	Items     []CAPoolItem `json:"items"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

// CAPoolItem 是调度组中的一个候选 CA。
type CAPoolItem struct {
	ID     int64 `json:"id"`
	PoolID int64 `json:"pool_id"`
	// CAValue 为内置 CA 标识或 acme-ca:<id>。
	CAValue  string `json:"ca_value"`
	Priority int    `json:"priority"`
	// Weight 预留给加权随机，当前等概率随机时忽略。
	Weight    int       `json:"weight"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// EnabledItems 返回启用中的候选，保持调用方给定的顺序。
func (p *CAPool) EnabledItems() []CAPoolItem {
	items := make([]CAPoolItem, 0, len(p.Items))
	for _, item := range p.Items {
		if item.Enabled {
			items = append(items, item)
		}
	}
	return items
}
