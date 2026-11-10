package service

import "strings"

// FilterAuthConfig 只保留部署类型声明过的认证与连接字段。
// 旧版本把部署路径等参数也存在凭据里，升级后保存凭据时会被过滤掉。
func FilterAuthConfig(def ProviderDef, cfg map[string]string) map[string]string {
	out := make(map[string]string, len(def.Fields))
	for _, field := range def.Fields {
		if value, ok := cfg[field.Key]; ok {
			out[field.Key] = value
		}
	}
	return out
}

// ExtractDeployConfig 从旧版凭据配置中提取部署参数字段，用于数据迁移和兼容旧客户端。
func ExtractDeployConfig(def ProviderDef, cfg map[string]string) map[string]string {
	out := make(map[string]string)
	for _, field := range def.DeployFields {
		if value, ok := cfg[field.Key]; ok && strings.TrimSpace(value) != "" {
			out[field.Key] = value
		}
	}
	// 兼容七牛云 Kodo 早期的单域名写法。
	if def.Value == "qiniu_kodo" && strings.TrimSpace(out["domains"]) == "" {
		if value := strings.TrimSpace(cfg["domain"]); value != "" {
			out["domains"] = value
		}
	}
	return out
}

// FilterDeployConfig 只保留部署类型声明过的部署参数字段。
func FilterDeployConfig(def ProviderDef, cfg map[string]string) map[string]string {
	out := make(map[string]string)
	for _, field := range def.DeployFields {
		if value, ok := cfg[field.Key]; ok {
			out[field.Key] = value
		}
	}
	return out
}

// MergeDeployConfig 合并凭据认证信息与部署节点参数。
// 部署节点中的同名参数优先；为兼容旧数据，凭据中残留的部署字段作为兜底。
func MergeDeployConfig(def ProviderDef, auth, deployCfg map[string]string) map[string]string {
	out := FilterAuthConfig(def, auth)
	for _, field := range def.DeployFields {
		if value, ok := auth[field.Key]; ok && strings.TrimSpace(value) != "" {
			out[field.Key] = value
		}
	}
	for _, field := range def.DeployFields {
		if value, ok := deployCfg[field.Key]; ok {
			out[field.Key] = value
		}
	}
	return out
}
