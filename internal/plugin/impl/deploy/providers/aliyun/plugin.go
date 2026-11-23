// Package aliyun 是合并后的阿里云部署目标分派器。
//
// 规范 ID aliyun 通过 target 选择 CDN、DCDN、ESA、OSS 或 WAF，
// 具体实现仍由 aliyuncdn、aliyuncloud、aliyunoss 提供，历史 ID 保持可用。
package aliyun

import (
	"context"
	"strings"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/protocol"
	"github.com/AXmishell/LACTSSL-Plugins/sdk/alibaba"
	"github.com/AXmishell/LACTSSL-Plugins/sdk/httpapi"
	"lactssl/internal/plugin/impl/deploy"
	"lactssl/internal/plugin/impl/deploy/providers/aliyuncdn"
	"lactssl/internal/plugin/impl/deploy/providers/aliyuncloud"
	"lactssl/internal/plugin/impl/deploy/providers/aliyunoss"
	"lactssl/internal/plugin/impl/deploy/util"
)

type plugin struct{}

func (plugin) ID() string        { return "aliyun" }
func (plugin) Aliases() []string { return nil }

func (plugin) Deploy(_ context.Context, req deploy.Request) error {
	cert := util.Certificate(req.Certificate)
	cfg := util.Merge(req.Auth, req.Config)
	switch strings.TrimSpace(cfg["target"]) {
	case "", "cdn":
		return aliyuncdn.DeployCDN(cert, cfg)
	case "dcdn":
		return aliyuncloud.DeployDCDN(cert, cfg)
	case "esa":
		return aliyuncloud.DeployESA(cert, cfg)
	case "oss":
		return aliyunoss.DeployOSS(cert, cfg)
	case "waf":
		return aliyuncloud.DeployWAF(cert, cfg)
	default:
		return i18n.E("service.deploy.deploy_target_invalid", "type", cfg["target"])
	}
}

// Verify 通过 STS GetCallerIdentity 只读校验 AccessKey。
// 目标资源权限（CDN/OSS/WAF 等）留给真实部署或插件后续扩展。
func (plugin) Verify(ctx context.Context, req deploy.Request) ([]protocol.VerifyCheck, error) {
	cfg := util.Merge(req.Auth, req.Config)
	loc := i18n.Normalize(req.Locale)
	accessKey := strings.TrimSpace(cfg["access_key_id"])
	secretKey := cfg["access_key_secret"]
	if accessKey == "" || secretKey == "" {
		return []protocol.VerifyCheck{{Key: "verify", Status: protocol.VerifyStatusFailed, Message: i18n.T(loc, "service.verify.deploy_failed", map[string]any{"error": i18n.T(loc, "service.deploy.aliyun_key_required", nil)})}}, nil
	}
	signer := alibaba.Signer{AccessKeyID: accessKey, AccessKeySecret: secretKey}
	client := httpapi.New("https://sts.aliyuncs.com")
	var identity map[string]any
	if err := signer.Do(ctx, client, "https://sts.aliyuncs.com", "GetCallerIdentity", "2015-04-01", map[string]string{}, &identity); err != nil {
		return []protocol.VerifyCheck{{Key: "verify", Status: protocol.VerifyStatusFailed, Message: i18n.T(loc, "service.verify.deploy_failed", map[string]any{"error": err.Error()})}}, nil
	}
	return []protocol.VerifyCheck{
		{Key: "connect", Status: protocol.VerifyStatusOK, Message: i18n.T(loc, "service.verify.connect_ok", nil)},
		{Key: "auth", Status: protocol.VerifyStatusOK, Message: i18n.T(loc, "service.verify.auth_ok", nil)},
		{Key: "scope", Status: protocol.VerifyStatusSkipped, Message: i18n.T(loc, "service.verify.scope_skipped", nil)},
	}, nil
}

func init() {
	deploy.MarkVerifiable("aliyun")
	deploy.Register(plugin{})
}
