// Package pluginproto 定义 LACTSSL 插件协议 v1 的线格式。
//
// 协议结构以外部插件 SDK 的 plugins/protocol 为唯一来源，本包只做
// type/const alias，避免宿主与 SDK 出现两份定义。
package pluginproto

import protocol "github.com/AXmishell/LACTSSL-Plugins/protocol"

// ProtocolVersion 是当前 helper 协议版本。
const ProtocolVersion = protocol.ProtocolVersion

// helper 进程的环境变量约定。
const (
	// MemoryLimitEnv 是主程序传入的常驻内存预算（字节）。
	MemoryLimitEnv = protocol.MemoryLimitEnv
	// ProtocolEnv 是主程序传入的协议版本。
	ProtocolEnv = protocol.ProtocolEnv
	// DeployFeaturesEnv 是主程序声明的部署插件特性列表（逗号分隔）。
	DeployFeaturesEnv = protocol.DeployFeaturesEnv
	// DNSFeaturesEnv 是主程序声明的 DNS 插件特性列表（逗号分隔）。
	DNSFeaturesEnv = protocol.DNSFeaturesEnv
)

const (
	FeatureMergedDeployers = protocol.FeatureMergedDeployers
	FeatureDynamicOptions  = protocol.FeatureDynamicOptions
	FeatureVerify          = protocol.FeatureVerify
	FeatureVerifyWrite     = protocol.FeatureVerifyWrite
	FeaturePlan            = protocol.FeaturePlan
)

const (
	VerifyStatusOK          = protocol.VerifyStatusOK
	VerifyStatusFailed      = protocol.VerifyStatusFailed
	VerifyStatusSkipped     = protocol.VerifyStatusSkipped
	VerifyStatusUnsupported = protocol.VerifyStatusUnsupported
	VerifyStatusError       = protocol.VerifyStatusError
)

type VerifyCheck = protocol.VerifyCheck
type VerifyResponse = protocol.VerifyResponse
