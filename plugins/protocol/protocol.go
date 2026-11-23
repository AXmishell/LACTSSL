// Package protocol 定义 LACTSSL 插件协议 v1 的线格式。
//
// 该包不依赖任何第三方库，主程序与插件仓库都直接引用它，
// 避免协议结构出现两份定义。
package protocol

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
	// DNSFeaturesEnv 是主程序声明的 DNS 插件特性列表（逗号分隔）。
	DNSFeaturesEnv = "LACTSSL_DNS_PLUGIN_FEATURES"
)

// FeatureMergedDeployers 告知部署 helper 只输出合并后的规范目标。
const FeatureMergedDeployers = "merged-deployers-v1"

// FeatureDynamicOptions 告知部署 helper 宿主支持 options 子命令和
// fields[].options_source / multiple 元数据。旧版宿主不会声明该特性，
// helper 因此继续输出与旧版完全一致的 manifest。
const FeatureDynamicOptions = "dynamic-options-v1"

// FeatureVerify 表示服务商/部署目标支持 verify 子命令的只读探测。
const FeatureVerify = "verify-v1"

// FeatureVerifyWrite 表示 verify 子命令支持 mode=write：在同一个 helper 进程内
// 完成临时 TXT 记录的写入与清理，复用 provider 实例、鉴权结果与连接。
// 宿主未声明该特性时，helper 继续只提供只读探测能力。
const FeatureVerifyWrite = "verify-write-v1"

// FeaturePlan 表示宿主支持 plan 子命令，可在批量部署前读取可部署对象。
const FeaturePlan = "plan-v1"

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
