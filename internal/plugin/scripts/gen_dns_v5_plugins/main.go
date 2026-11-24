//go:build ignore

// gen_dns_v5_plugins 生成 lego v5 DNS 服务商包装包、全量导入清单，
// 以及仓库 catalog 尚未覆盖的 provider 元数据。
//
// 用法：
//
//	go run scripts/gen_dns_v5_plugins/main.go
//
// 生成内容：
//   - providers/dns/lego5/providers/<id>/plugin.go：每个 lego v5 DNS 服务商的薄包装
//   - providers/dns/lego5/groups/all/all.go：空导入全部包装包
//   - providers/dns/lego5/meta/meta_gen.go：v5 全量 provider ID 与 catalog 缺失项
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"lactssl/providers/catalog"
)

const legoModule = "github.com/go-acme/lego/v5"

var (
	envKeyPattern    = regexp.MustCompile(`^[A-Z][A-Z0-9_]+$`)
	secretPattern    = regexp.MustCompile(`(?i)(KEY|TOKEN|SECRET|PASSWORD|CREDENTIAL)`)
	noiseFieldSuffix = regexp.MustCompile(`_(TTL|PROPAGATION_TIMEOUT|POLLING_INTERVAL|HTTP_TIMEOUT|SEQUENCE_INTERVAL|DNS_TIMEOUT)$`)
)

func main() {
	root := repoRoot()
	legoDir := moduleDir(root, legoModule)
	providersDir := filepath.Join(legoDir, "providers", "dns")

	entries, err := os.ReadDir(providersDir)
	check(err)

	aliases := parseAliases(filepath.Join(providersDir, "zz_gen_dns_providers.go"))

	var ids []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		if id == "internal" || id == "manual" {
			continue
		}
		if _, err := os.Stat(filepath.Join(providersDir, id, "dns_provider.go")); err != nil {
			if !hasNewDNSProvider(filepath.Join(providersDir, id)) {
				fmt.Fprintf(os.Stderr, "skip %s: no NewDNSProvider\n", id)
				continue
			}
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)

	outRoot := filepath.Join(root, "internal", "plugin", "providers", "dns", "lego5")
	// 清理上一次生成的目录，避免 provider 重命名后残留旧包。
	check(os.RemoveAll(filepath.Join(outRoot, "providers")))
	check(os.RemoveAll(filepath.Join(outRoot, "legacy")))
	check(os.RemoveAll(filepath.Join(outRoot, "groups")))
	current := make(map[string]bool, len(ids))
	var primaryIDs []string
	for _, id := range ids {
		primaryID := compatPrimaryID(id)
		current[primaryID] = true
		primaryIDs = append(primaryIDs, primaryID)
		info := readProviderInfo(filepath.Join(providersDir, id))
		writePlugin(filepath.Join(outRoot, "providers", primaryID, "plugin.go"), primaryID, id, compatAliases(id, primaryID, aliases[id]))
		extras = append(extras, buildExtra(primaryID, info))
	}
	sort.Strings(primaryIDs)

	// v5 移除了 7 个旧 provider。为了让新包完整覆盖旧 v4 包
	// 的 ID（安装时可回收旧 helper），保留同名占位条目；调用时返回明确错误。
	var legacyIDs []string
	for _, def := range catalog.DNSProviders() {
		if current[def.Value] {
			continue
		}
		legacyIDs = append(legacyIDs, def.Value)
		writeLegacy(filepath.Join(outRoot, "legacy", def.Value, "plugin.go"), def.Value, legacyHint(def.Value))
	}
	sort.Strings(legacyIDs)

	allIDs := append(append([]string(nil), primaryIDs...), legacyIDs...)
	writeAll(filepath.Join(outRoot, "groups", "all", "all.go"), primaryIDs, legacyIDs)
	writeGroup(filepath.Join(outRoot, "groups", "common", "all.go"), "common", commonProviderIDs,
		"常用 lego v5 DNS 服务商包装包", "该分组用于低体积/低内存构建，不追求覆盖全部上游 provider。")
	writeGroup(filepath.Join(outRoot, "groups", "minimal", "all.go"), "minimal", minimalProviderIDs,
		"最小可用 lego v5 DNS 服务商集合", "该分组只用于极简构建；默认发布不包含它。")
	writeMeta(filepath.Join(outRoot, "meta", "meta_gen.go"), allIDs)
	fmt.Printf("generated %d v5 provider plugins and %d legacy placeholders\n", len(ids), len(legacyIDs))
}

var extras []catalog.Provider

type providerInfo struct {
	Name        string
	Description string
	URL         string
	Credentials []tomlField
	Additional  []tomlField
}

type tomlField struct {
	Env   string
	Label string
}

func readProviderInfo(dir string) providerInfo {
	files, err := filepath.Glob(filepath.Join(dir, "*.toml"))
	if err != nil || len(files) == 0 {
		return providerInfo{}
	}
	return parseTOML(files[0])
}

func parseTOML(path string) providerInfo {
	raw, err := os.ReadFile(path)
	check(err)
	var info providerInfo
	section := ""
	inMultiline := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if count := strings.Count(line, "'''"); count > 0 {
			// 同一行成对出现的是空多行字符串（Description = ''''''），
			// 只有奇数个引号才表示进入或离开多行块。
			if count%2 == 1 {
				inMultiline = !inMultiline
			}
			continue
		}
		if inMultiline {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.Trim(line, "[]")
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch section {
		case "":
			switch key {
			case "Name":
				info.Name = tomlString(value)
			case "Description":
				info.Description = tomlString(value)
			case "URL":
				info.URL = tomlString(value)
			}
		case "Configuration.Credentials", "Configuration.Additional":
			if !envKeyPattern.MatchString(key) {
				continue
			}
			field := tomlField{Env: key, Label: tomlString(value)}
			if section == "Configuration.Credentials" {
				info.Credentials = append(info.Credentials, field)
			} else {
				info.Additional = append(info.Additional, field)
			}
		}
	}
	return info
}

func tomlString(value string) string {
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		if unquoted, err := strconv.Unquote(value); err == nil {
			return unquoted
		}
	}
	return strings.Trim(value, `"'`)
}

func buildExtra(id string, info providerInfo) catalog.Provider {
	if _, ok := catalog.LookupDNS(id); ok {
		return catalog.Provider{}
	}

	// v5 重命名的两个 provider 直接继承旧 catalog 的字段。
	if id == "dnsupdate" {
		if def, ok := catalog.LookupDNS("rfc2136"); ok {
			def.Value = id
			if info.Name != "" {
				def.Label = info.Name
			}
			if info.Description != "" {
				def.Desc = info.Description
			}
			return def
		}
	}
	if id == "webnamesru" {
		if def, ok := catalog.LookupDNS("webnames"); ok {
			def.Value = id
			if info.Name != "" {
				def.Label = info.Name
			}
			if info.Description != "" {
				def.Desc = info.Description
			}
			return def
		}
	}

	def := catalog.Provider{
		Value: id,
		Label: info.Name,
		Desc:  info.Description,
	}
	if def.Label == "" {
		def.Label = id
	}
	if def.Desc == "" {
		def.Desc = info.URL
	}
	def.Fields = buildFields(id, info)
	return def
}

func buildFields(id string, info providerInfo) []catalog.Field {
	if id == "exec" {
		return []catalog.Field{
			{Key: "exec_path", Label: "External program path", Type: "text", Required: true, Env: "EXEC_PATH"},
			{Key: "exec_mode", Label: "Exec mode (RAW or empty)", Type: "text", Env: "EXEC_MODE"},
		}
	}

	var fields []catalog.Field
	seen := map[string]bool{}
	add := func(item tomlField, required bool) {
		if noiseFieldSuffix.MatchString(item.Env) {
			return
		}
		key := strings.ToLower(item.Env)
		if seen[key] {
			key += "_2"
		}
		seen[key] = true
		label := item.Label
		if label == "" {
			label = item.Env
		}
		if strings.HasSuffix(item.Env, "_URL") || strings.HasSuffix(item.Env, "_ENDPOINT") {
			required = false
		}
		secret := secretPattern.MatchString(item.Env)
		fieldType := "text"
		if secret {
			fieldType = "password"
		}
		fields = append(fields, catalog.Field{
			Key:      key,
			Label:    label,
			Type:     fieldType,
			Required: required,
			Secret:   secret,
			Env:      item.Env,
		})
	}
	for _, item := range info.Credentials {
		add(item, true)
	}
	for _, item := range info.Additional {
		add(item, false)
	}
	return fields
}

func repoRoot() string {
	dir, err := os.Getwd()
	check(err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			fail("go.mod not found")
		}
		dir = parent
	}
}

func moduleDir(root, module string) string {
	// CI 的干净缓存里 go list -m 不会主动下载模块源码，先显式下载。
	download := exec.Command("go", "mod", "download", module)
	download.Dir = root
	_ = download.Run()

	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", module)
	cmd.Dir = root
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		fail(fmt.Sprintf("go list %s: %v: %s", module, err, out.String()))
	}
	return strings.TrimSpace(out.String())
}

// parseAliases 从 lego 自动生成的分发文件里解析 规范包名 -> 别名列表。
func parseAliases(path string) map[string][]string {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "解析 %s 失败，将不生成别名: %v\n", path, err)
		return nil
	}
	imports := map[string]string{}
	for _, spec := range file.Imports {
		pkgPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := filepath.Base(pkgPath)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		imports[name] = pkgPath
	}
	out := map[string][]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "NewDNSChallengeProviderByName" {
			return true
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			clause, ok := node.(*ast.CaseClause)
			if !ok {
				return true
			}
			var names []string
			for _, expr := range clause.List {
				lit, ok := expr.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				if value, err := strconv.Unquote(lit.Value); err == nil {
					names = append(names, value)
				}
			}
			if len(names) == 0 {
				return true
			}
			pkg := firstPackage(clause.Body, imports)
			if pkg == "" {
				return true
			}
			id := filepath.Base(pkg)
			for _, name := range names {
				if name != id && !contains(out[id], name) {
					out[id] = append(out[id], name)
				}
			}
			return true
		})
		return true
	})
	for id := range out {
		sort.Strings(out[id])
	}
	return out
}

func firstPackage(body []ast.Stmt, imports map[string]string) string {
	for _, stmt := range body {
		ret, ok := stmt.(*ast.ReturnStmt)
		if !ok || len(ret.Results) == 0 {
			continue
		}
		call, ok := ret.Results[0].(*ast.CallExpr)
		if !ok {
			continue
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		ident, ok := selector.X.(*ast.Ident)
		if !ok {
			continue
		}
		if pkg, found := imports[ident.Name]; found {
			return pkg
		}
	}
	return ""
}

func hasNewDNSProvider(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err == nil && bytes.Contains(data, []byte("func NewDNSProvider()")) {
			return true
		}
	}
	return false
}

// compatPrimaryID 保留旧 helper 的规范 ID，把 v5 的重命名作为别名。
// 这样旧账号、安装记录与 cleanupSupersededHelpers 的覆盖判断都能继续工作。
func compatPrimaryID(id string) string {
	switch id {
	case "dnsupdate":
		return "rfc2136"
	case "webnamesru":
		return "webnames"
	default:
		return id
	}
}

func compatAliases(legoID, primaryID string, aliases []string) []string {
	out := make([]string, 0, len(aliases)+1)
	for _, alias := range aliases {
		if alias != "" && alias != primaryID && !contains(out, alias) {
			out = append(out, alias)
		}
	}
	if legoID != primaryID && !contains(out, legoID) {
		out = append(out, legoID)
	}
	sort.Strings(out)
	return out
}

func writePlugin(path, id, legoID string, aliases []string) {
	var buf bytes.Buffer
	buf.WriteString("// Code generated by scripts/gen_dns_v5_plugins; DO NOT EDIT.\n\n")
	fmt.Fprintf(&buf, "// Package %s 注册 lego v5 %s DNS 服务商。\npackage %s\n\n", id, legoID, id)
	buf.WriteString("import (\n")
	fmt.Fprintf(&buf, "\tlego \"%s/providers/dns/%s\"\n", legoModule, legoID)
	buf.WriteString("\tdns \"lactssl/providers/api/dns\"\n")
	buf.WriteString("\tlego5 \"lactssl/providers/dns/lego5\"\n")
	buf.WriteString(")\n\n")
	buf.WriteString("type plugin struct{}\n\n")
	fmt.Fprintf(&buf, "func (plugin) ID() string { return %q }\n\n", id)
	if len(aliases) == 0 {
		buf.WriteString("func (plugin) Aliases() []string { return nil }\n\n")
	} else {
		buf.WriteString("func (plugin) Aliases() []string {\n\treturn []string{")
		for i, alias := range aliases {
			if i > 0 {
				buf.WriteString(", ")
			}
			fmt.Fprintf(&buf, "%q", alias)
		}
		buf.WriteString("}\n}\n\n")
	}
	buf.WriteString("func (plugin) New(map[string]string) (dns.NativeChallengeProvider, error) {\n")
	buf.WriteString("\tp, err := lego.NewDNSProvider()\n")
	buf.WriteString("\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	buf.WriteString("\treturn lego5.Wrap(p), nil\n")
	buf.WriteString("}\n\n")
	buf.WriteString("func init() { dns.RegisterNative(plugin{}) }\n")
	writeGo(path, buf.Bytes())
}

// commonProviderIDs 是常用 provider 白名单；新增/删除时同步更新 common 分组。
var commonProviderIDs = []string{
	"alidns", "azuredns", "bunny", "cloudflare", "digitalocean", "dnsimple",
	"gandiv5", "gcloud", "godaddy", "hetzner", "huaweicloud", "linode",
	"namecheap", "namesilo", "ovh", "pdns", "porkbun", "route53",
	"tencentcloud", "vultr",
}

// minimalProviderIDs 是极简构建使用的最小集合。
var minimalProviderIDs = []string{"cloudflare"}

func writeGroup(path, name string, ids []string, title, description string) {
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	var buf bytes.Buffer
	buf.WriteString("// Code generated by scripts/gen_dns_v5_plugins; DO NOT EDIT.\n\n")
	fmt.Fprintf(&buf, "// Package %s 空导入%s。\n//\n// %s\npackage %s\n\n", name, title, description, name)
	buf.WriteString("import (\n")
	for _, id := range sorted {
		fmt.Fprintf(&buf, "\t_ \"lactssl/providers/dns/lego5/providers/%s\"\n", id)
	}
	buf.WriteString(")\n")
	writeGo(path, buf.Bytes())
}

func writeAll(path string, ids, legacyIDs []string) {
	sorted := append([]string(nil), ids...)
	legacy := append([]string(nil), legacyIDs...)
	sort.Strings(sorted)
	sort.Strings(legacy)
	var buf bytes.Buffer
	buf.WriteString("// Code generated by scripts/gen_dns_v5_plugins; DO NOT EDIT.\n\n")
	buf.WriteString("// Package all 空导入全部 lego v5 DNS 服务商包装包。\npackage all\n\n")
	buf.WriteString("import (\n")
	for _, id := range legacy {
		fmt.Fprintf(&buf, "\t_ \"lactssl/providers/dns/lego5/legacy/%s\"\n", id)
	}
	for _, id := range sorted {
		fmt.Fprintf(&buf, "\t_ \"lactssl/providers/dns/lego5/providers/%s\"\n", id)
	}
	buf.WriteString(")\n")
	writeGo(path, buf.Bytes())
}

func writeLegacy(path, id, hint string) {
	var buf bytes.Buffer
	buf.WriteString("// Code generated by scripts/gen_dns_v5_plugins; DO NOT EDIT.\n\n")
	fmt.Fprintf(&buf, "// Package legacy%s 保留 lego v5 已移除的 provider ID。\npackage legacy%s\n\n", id, id)
	buf.WriteString("import (\n")
	buf.WriteString("\t\"fmt\"\n\n")
	buf.WriteString("\tdns \"lactssl/providers/api/dns\"\n")
	buf.WriteString(")\n\n")
	buf.WriteString("type plugin struct{}\n\n")
	fmt.Fprintf(&buf, "func (plugin) ID() string { return %q }\n\n", id)
	buf.WriteString("func (plugin) Aliases() []string { return nil }\n\n")
	buf.WriteString("func (plugin) New(map[string]string) (dns.NativeChallengeProvider, error) {\n")
	fmt.Fprintf(&buf, "\treturn nil, fmt.Errorf(%q)\n", hint)
	buf.WriteString("}\n\n")
	buf.WriteString("func init() { dns.RegisterNative(plugin{}) }\n")
	writeGo(path, buf.Bytes())
}

func legacyHint(id string) string {
	switch id {
	case "azure":
		return "lego v5 已移除 azure provider，请改用 azuredns"
	case "iij":
		return "lego v5 已移除 iij provider，请改用 iijdpf 或 lego v4 插件包"
	default:
		return "lego v5 已移除 " + id + " provider，请改用其他服务商或继续使用 lego v4 插件包"
	}
}

func writeMeta(path string, ids []string) {
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	var buf bytes.Buffer
	buf.WriteString("// Code generated by scripts/gen_dns_v5_plugins; DO NOT EDIT.\n\n")
	buf.WriteString("package meta\n\n")
	buf.WriteString("import \"lactssl/providers/catalog\"\n\n")
	buf.WriteString("var v5ProviderIDs = []string{\n")
	for _, id := range sorted {
		fmt.Fprintf(&buf, "\t%q,\n", id)
	}
	buf.WriteString("}\n\n")
	buf.WriteString("var extraProviders = []catalog.Provider{\n")
	sort.Slice(extras, func(i, j int) bool { return extras[i].Value < extras[j].Value })
	for _, def := range extras {
		if def.Value == "" {
			continue
		}
		fmt.Fprintf(&buf, "\t{\n")
		fmt.Fprintf(&buf, "\t\tValue: %q,\n", def.Value)
		if def.Label != "" {
			fmt.Fprintf(&buf, "\t\tLabel: %q,\n", def.Label)
		}
		if def.LabelKey != "" {
			fmt.Fprintf(&buf, "\t\tLabelKey: %q,\n", def.LabelKey)
		}
		if def.Desc != "" {
			fmt.Fprintf(&buf, "\t\tDesc: %q,\n", def.Desc)
		}
		if def.DescKey != "" {
			fmt.Fprintf(&buf, "\t\tDescKey: %q,\n", def.DescKey)
		}
		if len(def.Fields) > 0 {
			buf.WriteString("\t\tFields: []catalog.Field{\n")
			for _, field := range def.Fields {
				buf.WriteString("\t\t\t{")
				fmt.Fprintf(&buf, "Key: %q", field.Key)
				if field.Label != "" {
					fmt.Fprintf(&buf, ", Label: %q", field.Label)
				}
				if field.LabelKey != "" {
					fmt.Fprintf(&buf, ", LabelKey: %q", field.LabelKey)
				}
				if field.Type != "" {
					fmt.Fprintf(&buf, ", Type: %q", field.Type)
				}
				if field.Required {
					buf.WriteString(", Required: true")
				}
				if field.Secret {
					buf.WriteString(", Secret: true")
				}
				if field.Placeholder != "" {
					fmt.Fprintf(&buf, ", Placeholder: %q", field.Placeholder)
				}
				if field.Default != "" {
					fmt.Fprintf(&buf, ", Default: %q", field.Default)
				}
				if field.Env != "" {
					fmt.Fprintf(&buf, ", Env: %q", field.Env)
				}
				buf.WriteString("},\n")
			}
			buf.WriteString("\t\t},\n")
		}
		buf.WriteString("\t},\n")
	}
	buf.WriteString("}\n")
	writeGo(path, buf.Bytes())
}

func writeGo(path string, data []byte) {
	formatted, err := format.Source(data)
	if err != nil {
		fail(fmt.Sprintf("格式化 %s 失败: %v", path, err))
	}
	check(os.MkdirAll(filepath.Dir(path), 0o755))
	check(os.WriteFile(path, formatted, 0o644))
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func check(err error) {
	if err != nil {
		fail(err.Error())
	}
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
