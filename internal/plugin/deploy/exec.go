package deployplugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"lactssl/internal/plugin/exec"
	pluginproto "lactssl/internal/plugin/proto"
)

const (
	execProtocol      = "v1"
	execManifestLimit = 1 << 20 // 1 MB
	// manifestOutputKB 让 manifest 输出上限与 execManifestLimit 对齐；
	// 普通 deploy 调用仍使用配置的 max_output_kb。
	manifestOutputKB = 1024
)

var (
	idPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	keyPattern    = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
	localePattern = regexp.MustCompile(`^[A-Za-z]{2,3}(?:-[A-Za-z0-9]{2,8})*$`)
	fieldTypes    = map[string]bool{"": true, "text": true, "password": true, "textarea": true, "number": true, "select": true, "switch": true}
	maxDeployers  = 512
	maxFields     = 64
	maxLocales    = 32
	// maxOptions 是单次动态选项读取的上限，避免异常 helper 返回超大列表。
	maxOptions = 1000
)

// ExternalDeployer 是由 exec helper 提供的部署目标元数据。
type ExternalDeployer struct {
	ID              string            `json:"id"`
	Name            string            `json:"name,omitempty"`
	NameI18n        map[string]string `json:"name_i18n,omitempty"`
	Description     string            `json:"description,omitempty"`
	DescriptionI18n map[string]string `json:"description_i18n,omitempty"`
	Aliases         []string          `json:"aliases,omitempty"`
	Fields          []ManifestField   `json:"fields,omitempty"`
	DeployFields    []ManifestField   `json:"deploy_fields,omitempty"`
	Capabilities    []string          `json:"capabilities,omitempty"`
	Helper          string            `json:"helper"`
	Version         string            `json:"version,omitempty"`
	// Enabled 表示该部署目标已在管理界面启用并注册到运行时。
	Enabled bool `json:"enabled"`
	// FileEnabled 表示该目标所属的 helper 文件未被管理员停用。
	FileEnabled bool `json:"file_enabled"`
}

var externalState = struct {
	sync.RWMutex
	deployers map[string]ExternalDeployer
	helpers   int
}{
	deployers: make(map[string]ExternalDeployer),
}

// discoverMu 串行化重扫，避免两次扫描交错替换注册表。
var discoverMu sync.Mutex

// Discover 扫描插件目录并重建外部部署插件注册表。
// 目录不存在时视为未安装插件，不返回错误。
func Discover(cfg DiscoverConfig) DiscoverResult {
	discoverMu.Lock()
	defer discoverMu.Unlock()

	cfg = cfg.normalized()
	result := DiscoverResult{Dir: cfg.Dir}
	enabledSet := make(map[string]bool, len(cfg.EnabledDeployers))
	for _, id := range cfg.EnabledDeployers {
		if id = normalize(id); id != "" {
			enabledSet[id] = true
		}
	}
	// 文件级停用：连 manifest 都不探测，避免执行管理员已隔离的 helper。
	disabledSet := make(map[string]bool, len(cfg.DisabledHelpers))
	for _, name := range cfg.DisabledHelpers {
		if name = strings.TrimSpace(name); name != "" {
			disabledSet[strings.ToLower(name)] = true
		}
	}
	externalState.Lock()
	externalState.deployers = make(map[string]ExternalDeployer)
	externalState.helpers = 0
	externalState.Unlock()

	if strings.TrimSpace(cfg.Dir) == "" {
		replaceExternal(nil)
		return result
	}
	entries, err := os.ReadDir(cfg.Dir)
	if err != nil {
		if !os.IsNotExist(err) {
			result.Errors = append(result.Errors, fmt.Sprintf("读取部署插件目录失败: %v", err))
		}
		replaceExternal(nil)
		return result
	}

	// 先完整探测，再替换注册表，避免扫描中途失败留下半套插件。
	type candidate struct {
		path     string
		manifest Manifest
		digest   string
	}
	var candidates []candidate
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if disabledSet[strings.ToLower(name)] {
			result.DisabledFiles = append(result.DisabledFiles, name)
			continue
		}
		path := filepath.Join(cfg.Dir, name)
		// Lstat 拒绝符号链接；组或其他用户可写的文件一律不加载。
		info, err := os.Lstat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if !info.Mode().IsRegular() {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: 不是普通文件", name))
			continue
		}
		if info.Mode()&0o111 == 0 {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: 不是可执行文件", name))
			continue
		}
		// 部署 helper 会拿到证书私钥，比 DNS helper 更严格：组或其他用户可写都拒绝。
		if info.Mode()&0o022 != 0 {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: 插件文件允许组或其他用户写入，已拒绝加载", name))
			continue
		}
		if expected := strings.TrimSpace(cfg.ExpectedDigests[name]); expected != "" {
			digest, err := fileSHA256(path)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", name, err))
				continue
			}
			if !strings.EqualFold(expected, digest) {
				result.Errors = append(result.Errors, fmt.Sprintf("%s: 文件摘要与安装记录不一致，已拒绝加载", name))
				continue
			}
		}
		manifest, err := inspectHelper(path, cfg)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		digest, err := fileSHA256(path)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		if manifest.SHA256 != "" && !strings.EqualFold(manifest.SHA256, digest) {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: sha256 校验失败", name))
			continue
		}
		if err := validateManifest(manifest); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		candidates = append(candidates, candidate{path: path, manifest: manifest, digest: digest})
	}

	var plugins []Deployer
	helpers := make(map[string]bool)
	for _, item := range candidates {
		accepted := 0
		for _, deployer := range item.manifest.Deployers {
			if cfg.isReserved(deployer) {
				result.Skipped = append(result.Skipped, deployer.ID)
				continue
			}
			enabled := enabledSet[normalize(deployer.ID)]
			if enabled {
				plugins = append(plugins, &externalDeployer{
					helper:   item.path,
					digest:   item.digest,
					deployer: deployer,
					version:  item.manifest.Version,
					settings: execSettings{
						memoryLimitMB: cfg.MemoryLimitMB,
						timeout:       cfg.Timeout,
						maxOutputKB:   cfg.MaxOutputKB,
					},
				})
				result.Deployers++
			} else {
				result.Disabled = append(result.Disabled, deployer.ID)
			}
			externalState.Lock()
			externalState.deployers[normalize(deployer.ID)] = ExternalDeployer{
				ID:              deployer.ID,
				Name:            deployer.Name,
				NameI18n:        deployer.NameI18n,
				Description:     deployer.Description,
				DescriptionI18n: deployer.DescriptionI18n,
				Aliases:         deployer.Aliases,
				Fields:          deployer.Fields,
				DeployFields:    deployer.DeployFields,
				Capabilities:    deployer.Capabilities,
				Helper:          item.path,
				Version:         item.manifest.Version,
				Enabled:         enabled,
				FileEnabled:     true,
			}
			externalState.Unlock()
			accepted++
		}
		// 只统计真正提供了部署目标的 helper；被内置实现完全覆盖的不计。
		if accepted > 0 {
			helpers[item.path] = true
		}
	}

	_, errs := replaceExternal(plugins)
	for _, err := range errs {
		result.Errors = append(result.Errors, err.Error())
	}
	result.Helpers = len(helpers)
	externalState.Lock()
	externalState.helpers = result.Helpers
	externalState.Unlock()
	sort.Strings(result.Skipped)
	sort.Strings(result.Disabled)
	sort.Strings(result.DisabledFiles)
	return result
}

// ExternalDeployers 返回已探测到的全部外部部署目标，按 ID 排序。
// 包含被管理员停用的目标：它们保留元数据，但不注册到运行时。
// 通过 RegisterExternal 直接注册的实现也会出现（未经过 Discover 时没有停用状态）。
func ExternalDeployers() []ExternalDeployer {
	registered := make(map[string]ExternalDeployer)
	for _, plugin := range ListBySource(SourceExec) {
		if provider, ok := plugin.(interface{ Info() ExternalDeployer }); ok {
			info := provider.Info()
			info.Enabled = true
			registered[normalize(info.ID)] = info
		}
	}
	externalState.RLock()
	defer externalState.RUnlock()
	out := make([]ExternalDeployer, 0, len(externalState.deployers)+len(registered))
	seen := make(map[string]bool, len(externalState.deployers)+len(registered))
	for id, deployer := range externalState.deployers {
		seen[id] = true
		if info, ok := registered[id]; ok {
			deployer.Enabled = true
			deployer = info
		}
		out = append(out, deployer)
	}
	for id, deployer := range registered {
		if !seen[id] {
			out = append(out, deployer)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ExternalEnabledDeployers 返回已启用并注册到运行时的外部部署目标。
func ExternalEnabledDeployers() []ExternalDeployer {
	all := ExternalDeployers()
	out := all[:0]
	for _, deployer := range all {
		if deployer.Enabled {
			out = append(out, deployer)
		}
	}
	return out
}

// ExternalDeployerInfo 查找外部部署目标元数据，支持别名。
// 停用的目标同样可以查到，便于界面切换与历史凭据展示。
func ExternalDeployerInfo(id string) (ExternalDeployer, bool) {
	key := normalize(id)
	externalState.RLock()
	deployer, found := externalState.deployers[key]
	externalState.RUnlock()
	if found {
		return deployer, true
	}
	plugin, ok := Lookup(id)
	if !ok || plugin.Source() != SourceExec {
		return ExternalDeployer{}, false
	}
	if provider, ok := plugin.(interface{ Info() ExternalDeployer }); ok {
		info := provider.Info()
		// 通过 RegisterExternal 直接注册的实现不在扫描状态中，注册即视为启用。
		info.Enabled = true
		return info, true
	}
	return ExternalDeployer{}, false
}

// ExternalDeployHelpers 返回已加载的部署 helper 数量。
func ExternalDeployHelpers() int {
	externalState.RLock()
	defer externalState.RUnlock()
	return externalState.helpers
}

func inspectHelper(path string, cfg DiscoverConfig) (Manifest, error) {
	stdout, stderr, err := pluginexec.Run(context.Background(), path, []string{"manifest"}, nil, pluginexec.Options{
		Timeout:       cfg.ManifestTimeout,
		MemoryLimitMB: cfg.MemoryLimitMB,
		MaxOutputKB:   manifestOutputKB,
		Env: []string{
			"LACTSSL_DEPLOY_PLUGIN_PROTOCOL=" + execProtocol,
			"LACTSSL_DEPLOY_PLUGIN_FEATURES=" + FeatureMergedDeployers + "," + FeatureDynamicOptions + "," + pluginproto.FeatureVerify,
		},
	})
	if errors.Is(err, pluginexec.ErrTimeout) {
		return Manifest{}, fmt.Errorf("manifest 探测超时（%s）", cfg.ManifestTimeout)
	}
	var memoryErr *pluginexec.MemoryLimitError
	if errors.As(err, &memoryErr) {
		return Manifest{}, fmt.Errorf("manifest 探测内存占用超过上限 %d MB", cfg.MemoryLimitMB)
	}
	if err != nil {
		return Manifest{}, fmt.Errorf("manifest 探测失败: %v%s", err, stderrSuffix(stderr))
	}
	if len(stdout) > execManifestLimit {
		return Manifest{}, fmt.Errorf("manifest 输出超过 %d 字节", execManifestLimit)
	}
	var manifest Manifest
	if err := json.Unmarshal([]byte(stdout), &manifest); err != nil {
		return Manifest{}, fmt.Errorf("解析 manifest 失败: %v", err)
	}
	return manifest, nil
}

// InspectHelper 探测并校验单个 helper 文件，供插件上传接口在落盘前验证。
// 返回值中的 digest 是文件的实际 sha256，便于调用方记录与展示。
func InspectHelper(path string, cfg DiscoverConfig) (Manifest, string, error) {
	cfg = cfg.normalized()
	manifest, err := inspectHelper(path, cfg)
	if err != nil {
		return Manifest{}, "", err
	}
	digest, err := fileSHA256(path)
	if err != nil {
		return Manifest{}, "", err
	}
	if manifest.SHA256 != "" && !strings.EqualFold(manifest.SHA256, digest) {
		return Manifest{}, "", fmt.Errorf("manifest 声明的 sha256 与文件不一致")
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, "", err
	}
	return manifest, digest, nil
}

func validateManifest(manifest Manifest) error {
	if manifest.Protocol != execProtocol {
		return fmt.Errorf("不支持的插件协议 %q，需要 %q", manifest.Protocol, execProtocol)
	}
	if len(manifest.Deployers) == 0 {
		return fmt.Errorf("manifest 未声明任何部署目标")
	}
	if len(manifest.Deployers) > maxDeployers {
		return fmt.Errorf("manifest 部署目标数量超过 %d", maxDeployers)
	}
	if len(manifest.Name) > 128 || len(manifest.Version) > 64 {
		return fmt.Errorf("manifest 名称或版本号过长")
	}
	seen := map[string]bool{}
	for _, deployer := range manifest.Deployers {
		if !idPattern.MatchString(deployer.ID) {
			return fmt.Errorf("部署目标 ID 非法: %q", deployer.ID)
		}
		if len(deployer.Name) > 128 || len(deployer.Description) > 1024 {
			return fmt.Errorf("部署目标 %s 名称或描述过长", deployer.ID)
		}
		if err := validateI18nMap(deployer.ID, "名称", deployer.NameI18n, 128); err != nil {
			return err
		}
		if err := validateI18nMap(deployer.ID, "描述", deployer.DescriptionI18n, 1024); err != nil {
			return err
		}
		if len(deployer.Aliases) > 64 {
			return fmt.Errorf("部署目标 %s 的别名数量超过 64", deployer.ID)
		}
		if seen[deployer.ID] {
			return fmt.Errorf("部署目标 ID 重复: %q", deployer.ID)
		}
		seen[deployer.ID] = true
		for _, alias := range deployer.Aliases {
			if !idPattern.MatchString(alias) {
				return fmt.Errorf("部署目标 %s 的别名非法: %q", deployer.ID, alias)
			}
			if alias == deployer.ID {
				return fmt.Errorf("部署目标 %s 的别名不能与 ID 相同", deployer.ID)
			}
			if seen[alias] {
				return fmt.Errorf("部署目标 %s 的别名重复: %q", deployer.ID, alias)
			}
			seen[alias] = true
		}
		keys := map[string]bool{}
		if err := validateFields(deployer.ID, "认证", deployer.Fields, keys); err != nil {
			return err
		}
		if err := validateFields(deployer.ID, "部署", deployer.DeployFields, keys); err != nil {
			return err
		}
	}
	return nil
}

func validateFields(deployerID, kind string, fields []ManifestField, keys map[string]bool) error {
	if len(fields) > maxFields {
		return fmt.Errorf("部署目标 %s 的%s字段数量超过 %d", deployerID, kind, maxFields)
	}
	for _, field := range fields {
		if !keyPattern.MatchString(field.Key) {
			return fmt.Errorf("部署目标 %s 的%s字段 key 非法: %q", deployerID, kind, field.Key)
		}
		if keys[field.Key] {
			return fmt.Errorf("部署目标 %s 的字段 key 重复: %q", deployerID, field.Key)
		}
		keys[field.Key] = true
		if !fieldTypes[field.Type] {
			return fmt.Errorf("部署目标 %s 的字段 %s 类型非法: %q", deployerID, field.Key, field.Type)
		}
		if len(field.Label) > 128 || len(field.Help) > 1024 || len(field.Placeholder) > 256 {
			return fmt.Errorf("部署目标 %s 的字段 %s 文本过长", deployerID, field.Key)
		}
		if err := validateI18nMap(deployerID, "字段 "+field.Key+" 标签", field.LabelI18n, 128); err != nil {
			return err
		}
		if err := validateI18nMap(deployerID, "字段 "+field.Key+" 占位符", field.PlaceholderI18n, 256); err != nil {
			return err
		}
		if err := validateI18nMap(deployerID, "字段 "+field.Key+" 帮助", field.HelpI18n, 1024); err != nil {
			return err
		}
		if len(field.Options) > 64 {
			return fmt.Errorf("部署目标 %s 的字段 %s 选项过多", deployerID, field.Key)
		}
		for _, option := range field.Options {
			if len(option.Label) > 128 || len(option.Value) > 128 {
				return fmt.Errorf("部署目标 %s 的字段 %s 选项文本过长", deployerID, field.Key)
			}
			if err := validateI18nMap(deployerID, "字段 "+field.Key+" 选项 "+option.Value, option.LabelI18n, 128); err != nil {
				return err
			}
		}
		if field.OptionsSource != "" && !idPattern.MatchString(field.OptionsSource) {
			return fmt.Errorf("部署目标 %s 的字段 %s 动态选项来源非法: %q", deployerID, field.Key, field.OptionsSource)
		}
		if field.ShowIf != nil && !keyPattern.MatchString(field.ShowIf.Key) {
			return fmt.Errorf("部署目标 %s 的字段 %s 显示条件非法", deployerID, field.Key)
		}
		if field.ShowIf != nil {
			if len(field.ShowIf.Values) > 64 {
				return fmt.Errorf("部署目标 %s 的字段 %s 显示条件选项过多", deployerID, field.Key)
			}
			for _, value := range field.ShowIf.Values {
				if strings.TrimSpace(value) == "" || len(value) > 128 {
					return fmt.Errorf("部署目标 %s 的字段 %s 显示条件值非法", deployerID, field.Key)
				}
			}
		}
	}
	return nil
}

// validateI18nMap 校验 helper 提供的多语言文案表。
// 非法 locale 或过长文本直接拒绝整个 manifest，避免异常数据进入元数据缓存。
func validateI18nMap(deployerID, what string, values map[string]string, maxLen int) error {
	if len(values) > maxLocales {
		return fmt.Errorf("部署目标 %s 的%s语言数量超过 %d", deployerID, what, maxLocales)
	}
	for locale, text := range values {
		if len(locale) > 32 || !localePattern.MatchString(locale) {
			return fmt.Errorf("部署目标 %s 的%s语言标识非法: %q", deployerID, what, locale)
		}
		if len(text) > maxLen {
			return fmt.Errorf("部署目标 %s 的%s文本过长: %s", deployerID, what, locale)
		}
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// OptionsRequest 是一次动态选项读取请求。
// Auth 与 Config 来自目标凭据和当前部署节点参数，宿主不会把证书私钥传给插件。
type OptionsRequest struct {
	Field  string
	Source string
	Query  string
	Auth   map[string]string
	Config map[string]string
}

// ErrOptionsUnsupported 表示目标未实现动态选项读取。
var ErrOptionsUnsupported = errors.New("deploy target does not support dynamic options")

// ListOptions 调用外部部署 helper 的 options 子命令。
func ListOptions(ctx context.Context, id string, req OptionsRequest) ([]ManifestOption, error) {
	plugin, ok := Lookup(id)
	if !ok {
		return nil, fmt.Errorf("部署目标 %s 未启用", id)
	}
	lister, ok := plugin.(interface {
		ListOptions(context.Context, OptionsRequest) ([]ManifestOption, error)
	})
	if !ok {
		return nil, ErrOptionsUnsupported
	}
	return lister.ListOptions(ctx, req)
}

type execSettings struct {
	memoryLimitMB int
	timeout       time.Duration
	maxOutputKB   int
}

type externalDeployer struct {
	helper   string
	digest   string
	deployer ManifestDeployer
	version  string
	settings execSettings
}

func (d *externalDeployer) ID() string        { return d.deployer.ID }
func (d *externalDeployer) Aliases() []string { return d.deployer.Aliases }
func (d *externalDeployer) Source() Source    { return SourceExec }

// Info 返回插件元数据，供注册表在未经 Discover 时也能解析类型定义。
func (d *externalDeployer) Info() ExternalDeployer {
	return ExternalDeployer{
		ID:              d.deployer.ID,
		Name:            d.deployer.Name,
		NameI18n:        d.deployer.NameI18n,
		Description:     d.deployer.Description,
		DescriptionI18n: d.deployer.DescriptionI18n,
		Aliases:         d.deployer.Aliases,
		Fields:          d.deployer.Fields,
		DeployFields:    d.deployer.DeployFields,
		Capabilities:    d.deployer.Capabilities,
		Helper:          d.helper,
		Version:         d.version,
		Enabled:         true,
	}
}

type execRequest struct {
	Action      string            `json:"action"`
	Protocol    string            `json:"protocol"`
	Provider    string            `json:"provider"`
	Auth        map[string]string `json:"auth,omitempty"`
	Config      map[string]string `json:"config,omitempty"`
	Certificate Certificate       `json:"certificate"`
}

type execResult struct {
	OK      *bool  `json:"ok,omitempty"`
	Message string `json:"message,omitempty"`
}

// verifyDigest 在每次执行前重新校验 helper 摘要。
// 扫描与执行之间文件可能被替换，避免管理员批准的是旧文件、实际执行的却是新文件。
func (d *externalDeployer) verifyDigest() error {
	if d.digest == "" {
		return nil
	}
	current, err := fileSHA256(d.helper)
	if err != nil {
		return fmt.Errorf("插件 %s 无法读取 helper 文件: %v", d.ID(), err)
	}
	if current != d.digest {
		return fmt.Errorf("插件 %s 的 helper 文件已变化，请重扫插件目录后再试", d.ID())
	}
	return nil
}

func (d *externalDeployer) Deploy(ctx context.Context, req Request) error {
	if err := d.verifyDigest(); err != nil {
		return err
	}

	body, err := json.Marshal(execRequest{
		Action:      "deploy",
		Protocol:    execProtocol,
		Provider:    d.ID(),
		Auth:        cloneConfig(req.Auth),
		Config:      cloneConfig(req.Config),
		Certificate: req.Certificate,
	})
	if err != nil {
		return fmt.Errorf("编码插件请求失败: %w", err)
	}

	stdout, stderr, err := pluginexec.Run(ctx, d.helper, []string{"deploy"}, body, pluginexec.Options{
		Timeout:       d.settings.timeout,
		MemoryLimitMB: d.settings.memoryLimitMB,
		MaxOutputKB:   d.settings.maxOutputKB,
		Env:           []string{"LACTSSL_DEPLOY_PLUGIN_PROTOCOL=" + execProtocol},
	})
	secrets := redactionSecrets(req)
	if errors.Is(err, pluginexec.ErrTimeout) {
		return fmt.Errorf("插件 %s 部署超时（%s）", d.ID(), d.settings.timeout)
	}
	var memoryErr *pluginexec.MemoryLimitError
	if errors.As(err, &memoryErr) {
		return fmt.Errorf("插件 %s 内存占用超过上限 %d MB，已强制结束", d.ID(), d.settings.memoryLimitMB)
	}
	if err != nil {
		message := pluginexec.Redact(stderr, secrets)
		return fmt.Errorf("插件 %s 执行部署失败: %v%s", d.ID(), err, suffix(message))
	}

	output := strings.TrimSpace(stdout)
	if output == "" {
		return nil
	}
	var result execResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return fmt.Errorf("插件 %s 返回了非法结果: %s", d.ID(), pluginexec.Redact(pluginexec.Limit(output, 512), secrets))
	}
	if result.OK != nil && !*result.OK {
		message := strings.TrimSpace(result.Message)
		if message == "" {
			message = "插件未提供错误信息"
		}
		return fmt.Errorf("插件 %s 部署失败: %s", d.ID(), pluginexec.Redact(pluginexec.Limit(message, 512), secrets))
	}
	return nil
}

type execVerifyRequest struct {
	Action   string            `json:"action"`
	Protocol string            `json:"protocol"`
	Provider string            `json:"provider"`
	Auth     map[string]string `json:"auth,omitempty"`
	Config   map[string]string `json:"config,omitempty"`
	Mode     string            `json:"mode,omitempty"`
	Locale   string            `json:"locale,omitempty"`
}

func hasVerifyCapability(values []string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), pluginproto.FeatureVerify) {
			return true
		}
	}
	return false
}

// Verify 调用外部 helper 的 verify 子命令执行只读探测。
// 未声明 verify-v1 的旧 helper 直接返回 ErrVerifyUnsupported，不执行子进程。
func (d *externalDeployer) Verify(ctx context.Context, req Request) (pluginproto.VerifyResponse, error) {
	if !hasVerifyCapability(d.deployer.Capabilities) {
		return pluginproto.VerifyResponse{}, ErrVerifyUnsupported
	}
	if err := d.verifyDigest(); err != nil {
		return pluginproto.VerifyResponse{}, err
	}
	body, err := json.Marshal(execVerifyRequest{
		Action:   "verify",
		Protocol: execProtocol,
		Provider: d.ID(),
		Auth:     cloneConfig(req.Auth),
		Config:   cloneConfig(req.Config),
		Mode:     "read",
		Locale:   req.Locale,
	})
	if err != nil {
		return pluginproto.VerifyResponse{}, fmt.Errorf("编码插件验证请求失败: %w", err)
	}
	timeout := d.settings.timeout
	if timeout <= 0 || timeout > 60*time.Second {
		timeout = 60 * time.Second
	}
	stdout, stderr, err := pluginexec.Run(ctx, d.helper, []string{"verify"}, body, pluginexec.Options{
		Timeout:       timeout,
		MemoryLimitMB: d.settings.memoryLimitMB,
		MaxOutputKB:   d.settings.maxOutputKB,
		Env:           []string{"LACTSSL_DEPLOY_PLUGIN_PROTOCOL=" + execProtocol},
	})
	secrets := redactionSecrets(req)
	if errors.Is(err, pluginexec.ErrTimeout) {
		return pluginproto.VerifyResponse{}, fmt.Errorf("插件 %s 验证超时（%s）", d.ID(), timeout)
	}
	var memoryErr *pluginexec.MemoryLimitError
	if errors.As(err, &memoryErr) {
		return pluginproto.VerifyResponse{}, fmt.Errorf("插件 %s 内存占用超过上限 %d MB，已强制结束", d.ID(), d.settings.memoryLimitMB)
	}
	if err != nil {
		message := pluginexec.Redact(stderr, secrets)
		return pluginproto.VerifyResponse{}, fmt.Errorf("插件 %s 执行验证失败: %v%s", d.ID(), err, suffix(message))
	}
	output := strings.TrimSpace(stdout)
	if output == "" {
		return pluginproto.VerifyResponse{}, fmt.Errorf("插件 %s 未返回验证结果", d.ID())
	}
	var result pluginproto.VerifyResponse
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return pluginproto.VerifyResponse{}, fmt.Errorf("插件 %s 返回了非法验证结果: %s", d.ID(), pluginexec.Redact(pluginexec.Limit(output, 512), secrets))
	}
	if result.Checks == nil {
		result.Checks = []pluginproto.VerifyCheck{}
	}
	return result, nil
}

type execOptionsRequest struct {
	Action   string            `json:"action"`
	Protocol string            `json:"protocol"`
	Provider string            `json:"provider"`
	Field    string            `json:"field"`
	Source   string            `json:"source,omitempty"`
	Query    string            `json:"query,omitempty"`
	Auth     map[string]string `json:"auth,omitempty"`
	Config   map[string]string `json:"config,omitempty"`
}

type execOptionsResult struct {
	Options []ManifestOption `json:"options"`
	OK      *bool            `json:"ok,omitempty"`
	Message string           `json:"message,omitempty"`
}

// ListOptions 通过 options 子命令读取远端可选项。
// 交互式请求使用较短的超时，避免面板不可达时长时间阻塞界面。
func (d *externalDeployer) ListOptions(ctx context.Context, req OptionsRequest) ([]ManifestOption, error) {
	if err := d.verifyDigest(); err != nil {
		return nil, err
	}
	body, err := json.Marshal(execOptionsRequest{
		Action:   "options",
		Protocol: execProtocol,
		Provider: d.ID(),
		Field:    req.Field,
		Source:   req.Source,
		Query:    req.Query,
		Auth:     cloneConfig(req.Auth),
		Config:   cloneConfig(req.Config),
	})
	if err != nil {
		return nil, fmt.Errorf("编码插件选项请求失败: %w", err)
	}

	timeout := 30 * time.Second
	if d.settings.timeout > 0 && d.settings.timeout < timeout {
		timeout = d.settings.timeout
	}
	stdout, stderr, err := pluginexec.Run(ctx, d.helper, []string{"options"}, body, pluginexec.Options{
		Timeout:       timeout,
		MemoryLimitMB: d.settings.memoryLimitMB,
		MaxOutputKB:   512,
		Env:           []string{"LACTSSL_DEPLOY_PLUGIN_PROTOCOL=" + execProtocol},
	})
	secrets := redactionSecrets(Request{Auth: req.Auth, Config: req.Config})
	if errors.Is(err, pluginexec.ErrTimeout) {
		return nil, fmt.Errorf("插件 %s 读取选项超时（%s）", d.ID(), timeout)
	}
	var memoryErr *pluginexec.MemoryLimitError
	if errors.As(err, &memoryErr) {
		return nil, fmt.Errorf("插件 %s 内存占用超过上限 %d MB，已强制结束", d.ID(), d.settings.memoryLimitMB)
	}
	if err != nil {
		message := pluginexec.Redact(stderr, secrets)
		return nil, fmt.Errorf("插件 %s 读取选项失败: %v%s", d.ID(), err, suffix(message))
	}

	output := strings.TrimSpace(stdout)
	if output == "" {
		return nil, nil
	}
	var result execOptionsResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return nil, fmt.Errorf("插件 %s 返回了非法选项: %s", d.ID(), pluginexec.Redact(pluginexec.Limit(output, 512), secrets))
	}
	if result.OK != nil && !*result.OK {
		message := strings.TrimSpace(result.Message)
		if message == "" {
			message = "插件未提供错误信息"
		}
		return nil, fmt.Errorf("插件 %s 读取选项失败: %s", d.ID(), pluginexec.Redact(pluginexec.Limit(message, 512), secrets))
	}
	return sanitizeOptions(result.Options), nil
}

func sanitizeOptions(options []ManifestOption) []ManifestOption {
	if len(options) == 0 {
		return nil
	}
	out := make([]ManifestOption, 0, len(options))
	seen := make(map[string]bool, len(options))
	for _, option := range options {
		value := strings.TrimSpace(option.Value)
		label := strings.TrimSpace(option.Label)
		if value == "" || len(value) > 256 || len(label) > 256 {
			continue
		}
		if label == "" {
			label = value
		}
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, ManifestOption{Label: label, Value: value})
		if len(out) >= maxOptions {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// redactionSecrets 收集可能出现在 helper 输出中的敏感值。
// 私钥按整块和每一行分别登记，避免 helper 只打印部分内容时泄露。
func redactionSecrets(req Request) map[string]string {
	out := make(map[string]string, len(req.Auth)+len(req.Config)+4)
	for key, value := range req.Auth {
		out["auth."+key] = value
	}
	for key, value := range req.Config {
		out["config."+key] = value
	}
	out["key_pem"] = req.Certificate.KeyPEM
	for index, line := range strings.Split(req.Certificate.KeyPEM, "\n") {
		line = strings.TrimSpace(line)
		if len(line) >= 20 {
			out[fmt.Sprintf("key_line_%d", index)] = line
		}
	}
	return out
}

func cloneConfig(cfg map[string]string) map[string]string {
	if len(cfg) == 0 {
		return nil
	}
	out := make(map[string]string, len(cfg))
	for key, value := range cfg {
		out[key] = value
	}
	return out
}

func stderrSuffix(stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return ""
	}
	return ": " + pluginexec.Limit(stderr, 512)
}

func suffix(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}
	return ": " + message
}
