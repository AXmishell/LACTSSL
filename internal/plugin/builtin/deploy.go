//go:build !nobuiltin_deploy

package builtin

import (
	"context"
	"strings"
	"sync"

	plugi18n "github.com/AXmishell/LACTSSL-Plugins/i18n"
	plugproto "github.com/AXmishell/LACTSSL-Plugins/protocol"
	deployplugin "lactssl/internal/plugin/deploy"
	plugcatalog "lactssl/internal/plugin/impl/catalog"
	plugdeploy "lactssl/internal/plugin/impl/deploy"
	"lactssl/internal/plugin/impl/deploy/providers/panels"
	pluginproto "lactssl/internal/plugin/proto"
)

var deployOnce sync.Once

func loadDeploy() {
	deployOnce.Do(func() {
		panels.RegisterBaota()
		panels.RegisterOnePanel()
		panels.RegisterSafeline()
	})
}

type builtinDeployer struct {
	inner plugdeploy.Deployer
	info  deployplugin.ExternalDeployer
}

func (d *builtinDeployer) ID() string        { return d.info.ID }
func (d *builtinDeployer) Aliases() []string { return d.info.Aliases }
func (d *builtinDeployer) Source() deployplugin.Source {
	return deployplugin.SourceBuiltin
}
func (d *builtinDeployer) Info() deployplugin.ExternalDeployer { return d.info }

func (d *builtinDeployer) Deploy(ctx context.Context, req deployplugin.Request) error {
	return d.inner.Deploy(ctx, plugdeploy.Request{
		Auth:        cloneStringMap(req.Auth),
		Config:      cloneStringMap(req.Config),
		Certificate: convertCertificate(req.Certificate),
		Locale:      req.Locale,
	})
}

func (d *builtinDeployer) Verify(ctx context.Context, req deployplugin.Request) (pluginproto.VerifyResponse, error) {
	verifier, ok := d.inner.(plugdeploy.Verifier)
	if !ok {
		return pluginproto.VerifyResponse{}, deployplugin.ErrVerifyUnsupported
	}
	checks, err := verifier.Verify(ctx, plugdeploy.Request{
		Auth:        cloneStringMap(req.Auth),
		Config:      cloneStringMap(req.Config),
		Certificate: convertCertificate(req.Certificate),
		Locale:      req.Locale,
	})
	if err != nil {
		return pluginproto.VerifyResponse{}, err
	}
	out := pluginproto.VerifyResponse{OK: true, Checks: make([]pluginproto.VerifyCheck, 0, len(checks))}
	for _, check := range checks {
		if check.Status != pluginproto.VerifyStatusOK && check.Status != pluginproto.VerifyStatusSkipped {
			out.OK = false
		}
		out.Checks = append(out.Checks, pluginproto.VerifyCheck{
			Key:     check.Key,
			Status:  check.Status,
			Message: check.Message,
		})
	}
	return out, nil
}

func (d *builtinDeployer) ListOptions(ctx context.Context, req deployplugin.OptionsRequest) ([]deployplugin.ManifestOption, error) {
	lister, ok := d.inner.(plugdeploy.OptionLister)
	if !ok {
		return nil, deployplugin.ErrOptionsUnsupported
	}
	options, err := lister.ListOptions(ctx, plugdeploy.OptionsRequest{
		Field:  req.Field,
		Source: req.Source,
		Query:  req.Query,
		Auth:   cloneStringMap(req.Auth),
		Config: cloneStringMap(req.Config),
	})
	if err != nil {
		return nil, err
	}
	out := make([]deployplugin.ManifestOption, 0, len(options))
	for _, option := range options {
		out = append(out, pluginproto.DeployOption{
			Label:     option.Label,
			LabelI18n: cloneStringMap(option.LabelI18n),
			Value:     option.Value,
		})
	}
	return out, nil
}

func (d *builtinDeployer) Plan(ctx context.Context, req deployplugin.PlanRequest) (deployplugin.PlanResult, error) {
	planner, ok := d.inner.(plugdeploy.Planner)
	if !ok {
		return deployplugin.PlanResult{}, deployplugin.ErrPlanUnsupported
	}
	plan, err := planner.Plan(ctx, plugdeploy.PlanRequest{
		Auth:   cloneStringMap(req.Auth),
		Config: cloneStringMap(req.Config),
		Locale: req.Locale,
	})
	if err != nil {
		return deployplugin.PlanResult{}, err
	}
	out := deployplugin.PlanResult{Field: plan.Field, Multiple: plan.Multiple}
	out.Objects = make([]deployplugin.PlanObject, 0, len(plan.Objects))
	for _, object := range plan.Objects {
		out.Objects = append(out.Objects, deployplugin.PlanObject{
			Value:       object.Value,
			Name:        object.Name,
			Domains:     append([]string(nil), object.Domains...),
			Identifiers: append([]string(nil), object.Identifiers...),
		})
	}
	return out, nil
}

// SyncDeploy 重建内置部署目标注册。
// 返回注册错误；调用方应保留原有元数据并把错误暴露给管理员。
func SyncDeploy(available, enabled bool, ids map[string]bool) error {
	if !available {
		if err := deployplugin.ReplaceBuiltin(nil); err != nil {
			return err
		}
		deployplugin.SetBuiltinDeployers(nil)
		return nil
	}
	loadDeploy()
	all := plugdeploy.List()
	infos := make([]deployplugin.ExternalDeployer, 0, len(all))
	adapters := make([]*builtinDeployer, 0, len(all))
	for _, deployer := range all {
		id := strings.ToLower(strings.TrimSpace(deployer.ID()))
		if id == "" {
			continue
		}
		// 与部署 helper 的 merged-deployers-v1 行为一致：只暴露规范目标，
		// 历史 ID 由宿主 deployTargetMergeRules 迁移，不再作为独立条目。
		if def, ok := plugcatalog.LookupDeployer(id); !ok || def.Deprecated {
			continue
		}
		active := enabled && ids[id]
		info := buildDeployerInfo(deployer, active)
		infos = append(infos, info)
		if active {
			adapters = append(adapters, &builtinDeployer{inner: deployer, info: info})
		}
	}
	deployers := make([]deployplugin.Deployer, 0, len(adapters))
	for _, adapter := range adapters {
		deployers = append(deployers, adapter)
	}
	if err := deployplugin.ReplaceBuiltin(deployers); err != nil {
		return err
	}
	deployplugin.SetBuiltinDeployers(infos)
	return nil
}

func buildDeployerInfo(deployer plugdeploy.Deployer, enabled bool) deployplugin.ExternalDeployer {
	id := strings.ToLower(strings.TrimSpace(deployer.ID()))
	def, found := plugcatalog.LookupDeployer(id)
	info := deployplugin.ExternalDeployer{
		ID:          id,
		Aliases:     append([]string(nil), deployer.Aliases()...),
		Version:     "builtin",
		Enabled:     enabled,
		FileEnabled: true,
		Source:      string(deployplugin.SourceBuiltin),
	}
	if found {
		info.Name = def.LocalizedName(plugi18n.Default)
		info.Description = def.LocalizedDesc(plugi18n.Default)
		info.NameI18n = localizedProviderMap(def.LocalizedName)
		info.DescriptionI18n = localizedProviderMap(def.LocalizedDesc)
		info.Fields = convertDeployFields(def.Fields)
		info.DeployFields = convertDeployFields(def.DeployFields)
	}
	if info.Name == "" {
		info.Name = id
	}
	if plugdeploy.IsVerifiable(id) {
		info.Capabilities = append(info.Capabilities, pluginproto.FeatureVerify)
	}
	if plugdeploy.IsPlannable(id) {
		info.Capabilities = append(info.Capabilities, pluginproto.FeaturePlan)
	}
	if hasDynamicOptions(info.Fields) || hasDynamicOptions(info.DeployFields) {
		info.Capabilities = append(info.Capabilities, pluginproto.FeatureDynamicOptions)
	}
	return info
}

func convertDeployFields(fields []plugcatalog.Field) []pluginproto.DeployField {
	out := make([]pluginproto.DeployField, 0, len(fields))
	for _, field := range fields {
		item := pluginproto.DeployField{
			Key:             field.Key,
			Label:           field.LocalizedLabel(plugi18n.Default),
			LabelI18n:       localizedFieldMap(field.LocalizedLabel),
			Type:            field.Type,
			Required:        field.Required,
			Secret:          field.Secret,
			Placeholder:     field.LocalizedPlaceholder(plugi18n.Default),
			PlaceholderI18n: localizedFieldMap(field.LocalizedPlaceholder),
			Help:            field.LocalizedHelp(plugi18n.Default),
			HelpI18n:        localizedFieldMap(field.LocalizedHelp),
			Default:         field.Default,
			OptionsSource:   field.OptionsSource,
			Multiple:        field.Multiple,
		}
		if field.ShowIf != nil {
			item.ShowIf = &pluginproto.DeployCondition{
				Key:    field.ShowIf.Key,
				Value:  field.ShowIf.Value,
				Values: append([]string(nil), field.ShowIf.Values...),
			}
		}
		for _, option := range field.Options {
			item.Options = append(item.Options, pluginproto.DeployOption{
				Label:     option.LocalizedLabel(plugi18n.Default),
				LabelI18n: localizedOptionMap(option.LocalizedLabel),
				Value:     option.Value,
			})
		}
		out = append(out, item)
	}
	return out
}

func convertCertificate(cert deployplugin.Certificate) plugproto.Certificate {
	return plugproto.Certificate{
		Domain:       cert.Domain,
		Domains:      append([]string(nil), cert.Domains...),
		Issuer:       cert.Issuer,
		Serial:       cert.Serial,
		NotBefore:    cert.NotBefore,
		NotAfter:     cert.NotAfter,
		CertPEM:      cert.CertPEM,
		FullchainPEM: cert.FullchainPEM,
		KeyPEM:       cert.KeyPEM,
	}
}

func hasDynamicOptions(fields []pluginproto.DeployField) bool {
	for _, field := range fields {
		if field.OptionsSource != "" {
			return true
		}
	}
	return false
}
