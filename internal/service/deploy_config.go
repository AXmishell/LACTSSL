package service

import "strings"

// FilterAuthConfig 只保留部署类型声明过的认证与连接字段。
// 旧版本把部署路径等参数也存在凭据里，升级后保存凭据时会被过滤掉；
// 但合并目标的部署对象（target/domain_type）是凭据级兜底，必须保留，
// 否则只提交 deploy_ids 的旧任务会回退到默认对象或部署失败。
func FilterAuthConfig(def ProviderDef, cfg map[string]string) map[string]string {
	out := make(map[string]string, len(def.Fields)+1)
	for _, field := range def.Fields {
		if value, ok := cfg[field.Key]; ok {
			out[field.Key] = value
		}
	}
	preserveDeployMode(def, cfg, out)
	preserveLegacyDeployRegion(def, cfg, out)
	return out
}

// preserveLegacyDeployRegion 保留旧凭据中的区域。
// 新版把 region 移到任务部署节点，但历史账号可能只把区域保存在凭据中；
// 保存凭据时继续保留，作为部署节点未填写区域时的兼容兜底。
func preserveLegacyDeployRegion(def ProviderDef, src, dst map[string]string) {
	if def.Value != "aliyun" && def.Value != "volcengine" {
		return
	}
	if !hasField(def.DeployFields, "region") {
		return
	}
	if value, found := src["region"]; found && strings.TrimSpace(value) != "" {
		dst["region"] = value
	}
}

func hasField(fields []FieldDef, key string) bool {
	for _, field := range fields {
		if field.Key == key {
			return true
		}
	}
	return false
}

// preserveDeployMode 保留合并目标在凭据中的部署对象字段。
func preserveDeployMode(def ProviderDef, src, dst map[string]string) {
	mode, ok := deployTargetModes[def.Value]
	if !ok {
		return
	}
	if value, found := src[mode.Key]; found && strings.TrimSpace(value) != "" {
		dst[mode.Key] = value
	}
}

// ExtractDeployConfig 从旧版凭据配置中提取部署参数字段，用于数据迁移和兼容旧客户端。
func ExtractDeployConfig(def ProviderDef, cfg map[string]string) map[string]string {
	out := make(map[string]string)
	for _, field := range def.DeployFields {
		if value, ok := cfg[field.Key]; ok && strings.TrimSpace(value) != "" {
			out[field.Key] = value
		}
	}
	// 兼容七牛云 Kodo 早期的单域名写法。合并后的 qiniu 目标同样接受
	// 历史凭据中的 domain 字段，避免迁移顺序变化导致配置丢失。
	if (def.Value == "qiniu_kodo" || def.Value == "qiniu") && strings.TrimSpace(out["domains"]) == "" {
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
	// 阿里云 WAF 的 region 历史上保存在任务部署参数中；合并后的 aliyun
	// 把 region 统一放在凭据字段，但旧任务里的值仍要保留给插件读取。
	if def.Value == "aliyun" {
		if value, ok := cfg["region"]; ok && strings.TrimSpace(value) != "" {
			out["region"] = value
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
		// 空字符串视为“未提供”：旧任务/旧客户端会用空 map 覆盖凭据里的
		// 部署对象兜底，导致跳转到默认目标。只有显式非空值才覆盖。
		if value, ok := deployCfg[field.Key]; ok && strings.TrimSpace(value) != "" {
			out[field.Key] = value
		}
	}
	return out
}

// MaskProviderConfig 返回脱敏后的凭据配置，密钥字段统一置空。
// 非密钥字段（主机、用户名、路径等）保留，方便前端展示和编辑。
func MaskProviderConfig(def ProviderDef, cfg map[string]string) map[string]string {
	out := make(map[string]string, len(def.Fields))
	for _, field := range def.Fields {
		value, ok := cfg[field.Key]
		if !ok {
			continue
		}
		if field.IsSecret() && strings.TrimSpace(value) != "" {
			out[field.Key] = ""
			continue
		}
		out[field.Key] = value
	}
	preserveDeployMode(def, cfg, out)
	return out
}

// MergeProviderConfig 合并更新请求与已有凭据。
// 密钥字段提交空值表示保留原值，避免脱敏后的表单误清空密钥。
func MergeProviderConfig(def ProviderDef, current, incoming map[string]string) map[string]string {
	out := make(map[string]string, len(def.Fields)+1)
	for _, field := range def.Fields {
		value, hasIncoming := incoming[field.Key]
		if field.IsSecret() && (!hasIncoming || strings.TrimSpace(value) == "") {
			if existing, ok := current[field.Key]; ok {
				out[field.Key] = existing
			}
			continue
		}
		if hasIncoming {
			out[field.Key] = value
		} else if existing, ok := current[field.Key]; ok {
			out[field.Key] = existing
		}
	}
	preserveDeployMode(def, current, out)
	preserveDeployMode(def, incoming, out)
	preserveLegacyDeployRegion(def, current, out)
	preserveLegacyDeployRegion(def, incoming, out)
	return out
}
