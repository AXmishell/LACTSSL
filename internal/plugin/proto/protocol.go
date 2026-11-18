// Package pluginproto 定义 LACTSSL 插件协议 v1 的线格式。
//
// 该包是宿主侧的协议实现，独立于 LACTSSL-Plugins 仓库，避免主程序在构建期
// 依赖插件源码。协议字段必须与插件仓库的 protocol 包保持一致；任何新增字段
// 都必须是可选的，破坏性变更需要提升协议版本。
package pluginproto

// ProtocolVersion 是当前 helper 协议版本。
const ProtocolVersion = "v1"

// helper 进程的环境变量约定。
const (
	// MemoryLimitEnv 是主程序传入的常驻内存预算（字节）。
	MemoryLimitEnv = "LACTSSL_PLUGIN_MEMORY_LIMIT"
	// ProtocolEnv 是主程序传入的协议版本。
	ProtocolEnv = "LACTSSL_PLUGIN_PROTOCOL"
	// DeployFeaturesEnv 是主程序声明的部署插件特性列表（逗号分隔）。
	DeployFeaturesEnv = "LACTSSL_DEPLOY_PLUGIN_FEATURES"
)

// FeatureMergedDeployers 告知部署 helper 只输出合并后的规范目标。
const FeatureMergedDeployers = "merged-deployers-v1"

// FeatureDynamicOptions 告知部署 helper 宿主支持 options 子命令和
// fields[].options_source / multiple 元数据。旧版宿主不会声明该特性。
const FeatureDynamicOptions = "dynamic-options-v1"

// FeatureVerify 告知 helper 宿主支持 verify 子命令与结构化检查结果。
// helper 在 manifest 中按服务商/部署目标声明该能力；未声明的目标仍按
// 旧流程处理（DNS 回退写测试，部署目标显示不支持验证）。
const FeatureVerify = "verify-v1"

// 验证检查项状态。
const (
	VerifyStatusOK          = "ok"
	VerifyStatusFailed      = "failed"
	VerifyStatusSkipped     = "skipped"
	VerifyStatusUnsupported = "unsupported"
	VerifyStatusError       = "error"
)

// VerifyCheck 是一项只读探测结果。
type VerifyCheck struct {
	Key     string `json:"key"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// VerifyResponse 是 verify 子命令的 stdout 响应。
type VerifyResponse struct {
	OK     bool          `json:"ok"`
	Checks []VerifyCheck `json:"checks"`
}
