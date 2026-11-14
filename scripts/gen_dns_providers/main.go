//go:build ignore

// gen_dns_providers 从 lego 源码生成内置 DNS 服务商定义。
//
// 用法：
//
//	go run scripts/gen_dns_providers/main.go
//
// 生成内容：
//   - internal/service/dns_providers_gen.go：未在 meta.go 中手工维护的服务商
//   - internal/i18n/locales/{zh-CN,en-US}.json：对应服务商的名称与说明
//
// 手工维护的服务商优先，生成器会自动跳过它们。
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

type fieldDef struct {
	Key      string
	Label    string
	LabelKey string
	Type     string
	Required bool
	Secret   bool
	Env      string
}

type providerDef struct {
	Value  string
	Name   string
	Fields []fieldDef
}

// 不纳入内置表单的服务商：manual 需要人工在终端添加 TXT 记录，无法后端自动完成。
var skipProviders = map[string]bool{
	"manual": true,
}

// lego 包注释缺失或名称不理想时的展示名覆盖。
var displayNameOverrides = map[string]string{
	"acmedns":      "acme-dns",
	"autodns":      "AutoDNS",
	"azure":        "Azure DNS (legacy)",
	"bluecat":      "Bluecat Address Manager",
	"brandit":      "Brandit",
	"civo":         "Civo",
	"com35":        "35.com",
	"corenetworks": "Core Networks",
	"ddnss":        "DDNSS",
	"designate":    "Designate DNSaaS for OpenStack",
	"directadmin":  "DirectAdmin",
	"gcore":        "Gcore",
	"httpreq":      "HTTP Request",
	"hurricane":    "Hurricane Electric",
	"infoblox":     "Infoblox DNS",
	"inwx":         "INWX Domain Robot",
	"ionos":        "IONOS / 1&1",
	"ionoscloud":   "IONOS Cloud",
	"myaddr":       "myaddr",
	"netcup":       "Netcup DNS",
	"selfhostde":   "SelfHost",
	"shellrent":    "Shellrent",
	"ultradns":     "UltraDNS",
	"wedos":        "WEDOS",
}

// 常见的字段环境变量后缀 -> 已有翻译键。
var labelKeyBySuffix = map[string]string{
	"API_KEY":               "meta.field.api_key.label.godaddy",
	"API_TOKEN":             "meta.field.api_token.label",
	"TOKEN":                 "meta.field.token.label",
	"ACCESS_TOKEN":          "meta.field.token.label",
	"API_SECRET":            "meta.field.api_secret.label",
	"SECRET":                "meta.field.api_secret.label",
	"SECRET_KEY":            "meta.field.secret_key.label",
	"ACCESS_KEY":            "meta.field.access_key.label",
	"ACCESS_KEY_ID":         "meta.field.access_key_id.label",
	"ACCESS_KEY_SECRET":     "meta.field.access_key_secret.label",
	"SECRET_ACCESS_KEY":     "meta.field.secret_access_key.label",
	"SECRET_ID":             "meta.field.secret_id.label",
	"USERNAME":              "meta.field.username.label",
	"USER":                  "meta.field.username.label",
	"PASSWORD":              "meta.field.password.label",
	"EMAIL":                 "meta.field.email.label",
	"REGION":                "meta.field.region.label",
	"REGION_ID":             "meta.field.region_id.label",
	"BASE_URL":              "meta.field.base_url.label",
	"API_URL":               "meta.field.api_url.label",
	"ZONE_NAME":             "meta.field.zone_name.label",
	"PROJECT":               "meta.field.project.label",
	"PERSONAL_ACCESS_TOKEN": "meta.field.personal_access_token.label",
}

// 过滤 lego 的通用调优变量，它们不应出现在凭据表单中。
var tuningSuffixes = []string{
	"_TTL",
	"_PROPAGATION_TIMEOUT",
	"_POLLING_INTERVAL",
	"_POLLING_TIMEOUT",
	"_SEQUENCE_INTERVAL",
	"_HTTP_TIMEOUT",
	"_HTTP_CLIENT_TIMEOUT",
	"_API_REQUEST_TIMEOUT",
	"_TIMEOUT",
	"_PROPAGATION_WAIT",
	"_DEBUG",
	"_CNAME_SUPPORT",
	"_DISABLE_CNAME_SUPPORT",
	"_DISABLE_COMPLETE_PROPAGATION",
	"_DNS_RESOLVERS",
	"_DNS_SERVERS",
	"_RESOLVERS",
	"_FILE",
}

func main() {
	root, err := os.Getwd()
	check(err)

	legoDir, err := moduleDir(root, "github.com/go-acme/lego/v4")
	check(err)
	providersDir := filepath.Join(legoDir, "providers", "dns")

	curated, err := curatedProviders(filepath.Join(root, "internal", "service", "meta.go"))
	check(err)

	entries, err := os.ReadDir(providersDir)
	check(err)

	var generated []providerDef
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		value := entry.Name()
		if value == "internal" || skipProviders[value] || curated[value] {
			continue
		}
		dir := filepath.Join(providersDir, value)
		name, fields := analyzeProvider(dir)
		if override, ok := displayNameOverrides[value]; ok {
			name = override
		}
		if name == "" {
			name = fallbackName(value)
		}
		generated = append(generated, providerDef{Value: value, Name: name, Fields: fields})
	}
	sort.Slice(generated, func(i, j int) bool { return generated[i].Value < generated[j].Value })

	writeGenerated(filepath.Join(root, "internal", "service", "dns_providers_gen.go"), generated)
	updateLocale(filepath.Join(root, "internal", "i18n", "locales", "zh-CN.json"), generated, true)
	updateLocale(filepath.Join(root, "internal", "i18n", "locales", "en-US.json"), generated, false)

	fmt.Printf("generated %d providers (%d fields)\n", len(generated), countFields(generated))
}

func moduleDir(root, module string) (string, error) {
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", module)
	cmd.Dir = root
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go list %s: %v: %s", module, err, strings.TrimSpace(out.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// curatedProviders 读取 meta.go 中已有的 DNSProviders，生成时跳过这些服务商。
func curatedProviders(path string) (map[string]bool, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		decl, ok := n.(*ast.GenDecl)
		if !ok || decl.Tok != token.VAR {
			return true
		}
		for _, spec := range decl.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range valueSpec.Names {
				if name.Name != "DNSProviders" || i >= len(valueSpec.Values) {
					continue
				}
				composite, ok := valueSpec.Values[i].(*ast.CompositeLit)
				if !ok {
					continue
				}
				for _, element := range composite.Elts {
					item, ok := element.(*ast.CompositeLit)
					if !ok {
						continue
					}
					for _, field := range item.Elts {
						kv, ok := field.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						key, ok := kv.Key.(*ast.Ident)
						if !ok || key.Name != "Value" {
							continue
						}
						lit, ok := kv.Value.(*ast.BasicLit)
						if !ok || lit.Kind != token.STRING {
							continue
						}
						value, err := strconv.Unquote(lit.Value)
						if err == nil {
							out[value] = true
						}
					}
				}
			}
		}
		return true
	})
	return out, nil
}

func analyzeProvider(dir string) (string, []fieldDef) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", nil
	}

	fset := token.NewFileSet()
	var files []*ast.File
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, entry.Name()), nil, parser.ParseComments)
		if err != nil {
			continue
		}
		files = append(files, file)
	}

	consts := stringConsts(files)

	type usage struct {
		env      string
		pos      token.Pos
		required bool
	}
	seen := map[string]usage{}
	getCalls := 0
	getArgs := 0

	record := func(expr ast.Expr, required bool, fallback token.Pos) {
		for _, env := range stringsInExpr(expr, consts) {
			if !validEnv(env) {
				continue
			}
			pos := expr.Pos()
			if pos == token.NoPos {
				pos = fallback
			}
			if old, ok := seen[env]; ok {
				if required {
					old.required = true
					seen[env] = old
				}
				continue
			}
			seen[env] = usage{env: env, pos: pos, required: required}
		}
	}

	// 先收集 env.Get 调用，判断哪些变量是必填。
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := selector.X.(*ast.Ident)
			if !ok || pkg.Name != "env" {
				return true
			}
			if selector.Sel.Name == "Get" {
				getCalls++
				getArgs += len(call.Args)
				for _, arg := range call.Args {
					record(arg, true, call.Pos())
				}
			}
			return true
		})
	}

	// 再收集所有 env.* 调用的参数，以及以 Env 开头的字符串常量。
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := selector.X.(*ast.Ident)
			if !ok || pkg.Name != "env" {
				return true
			}
			for _, arg := range envCallArgs(selector.Sel.Name, call) {
				record(arg, false, call.Pos())
			}
			return true
		})
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				valueSpec, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range valueSpec.Names {
					if !strings.HasPrefix(name.Name, "Env") || i >= len(valueSpec.Values) {
						continue
					}
					env, ok := evalString(valueSpec.Values[i], consts)
					if ok && validEnv(env) {
						if _, exists := seen[env]; !exists {
							seen[env] = usage{env: env, pos: valueSpec.Pos()}
						}
					}
				}
			}
		}
	}

	usages := make([]usage, 0, len(seen))
	for _, item := range seen {
		usages = append(usages, item)
	}
	sort.Slice(usages, func(i, j int) bool {
		if usages[i].pos == usages[j].pos {
			return usages[i].env < usages[j].env
		}
		return usages[i].pos < usages[j].pos
	})

	// 仅在“只有一组必填凭据”时强制校验：单字段服务商，或 env.Get 参数覆盖全部字段。
	// 存在多组认证方式（如 API Key 与用户名密码二选一）时不强制，避免阻塞合法配置。
	allRequired := len(usages) == 1 || (getCalls == 1 && getArgs == len(usages))
	envNames := make([]string, 0, len(usages))
	for _, item := range usages {
		envNames = append(envNames, item.env)
	}
	namespace := commonNamespace(envNames)

	fields := make([]fieldDef, 0, len(usages))
	for _, item := range usages {
		suffix := strings.TrimPrefix(item.env, namespace)
		if suffix == "" {
			suffix = item.env
		}
		field := fieldDef{
			Key:      strings.ToLower(item.env),
			Env:      item.env,
			Type:     "text",
			Required: allRequired,
		}
		if key, ok := labelKeyBySuffix[suffix]; ok {
			field.LabelKey = key
		} else {
			field.Label = humanize(suffix)
		}
		if isTextarea(suffix) {
			field.Type = "textarea"
			field.Secret = true
		} else if isSecret(suffix) {
			field.Type = "password"
		}
		fields = append(fields, field)
	}

	return providerDisplayName(files), fields
}

func providerDisplayName(files []*ast.File) string {
	for _, file := range files {
		if file.Doc == nil {
			continue
		}
		text := strings.TrimSpace(file.Doc.Text())
		if !strings.HasPrefix(text, "Package ") {
			continue
		}
		if index := strings.Index(text, "\n"); index >= 0 {
			text = strings.TrimSpace(text[:index])
		}
		lower := strings.ToLower(text)
		for _, marker := range []string{" using ", " through "} {
			index := strings.Index(lower, marker)
			if index < 0 {
				continue
			}
			name := strings.TrimSpace(text[index+len(marker):])
			name = strings.TrimSuffix(name, ".")
			name = strings.TrimSpace(name)
			for _, prefix := range []string{"the ", "a ", "an "} {
				name = strings.TrimPrefix(name, prefix)
			}
			for _, suffix := range []string{" DNS api", " api"} {
				name = strings.TrimSuffix(name, suffix)
			}
			return titleFirst(strings.TrimSpace(name))
		}
	}
	return ""
}

func titleFirst(value string) string {
	if value == "" || strings.Contains(value, ".") {
		return value
	}
	runes := []rune(value)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func fallbackName(value string) string {
	// 常见缩写保持大写。
	replacer := strings.NewReplacer("dns", "DNS", "api", "API", "ip", "IP", "url", "URL")
	value = replacer.Replace(value)
	if value == "" {
		return value
	}
	runes := []rune(value)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func stringConsts(files []*ast.File) map[string]string {
	consts := map[string]string{}
	for pass := 0; pass < 8; pass++ {
		changed := false
		for _, file := range files {
			for _, decl := range file.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.CONST {
					continue
				}
				for _, spec := range gen.Specs {
					valueSpec, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, name := range valueSpec.Names {
						if i >= len(valueSpec.Values) {
							continue
						}
						value, ok := evalString(valueSpec.Values[i], consts)
						if !ok {
							continue
						}
						if consts[name.Name] != value {
							consts[name.Name] = value
							changed = true
						}
					}
				}
			}
		}
		if !changed {
			break
		}
	}
	return consts
}

func evalString(expr ast.Expr, consts map[string]string) (string, bool) {
	switch value := expr.(type) {
	case *ast.BasicLit:
		if value.Kind != token.STRING {
			return "", false
		}
		text, err := strconv.Unquote(value.Value)
		return text, err == nil
	case *ast.Ident:
		text, ok := consts[value.Name]
		return text, ok
	case *ast.BinaryExpr:
		if value.Op != token.ADD {
			return "", false
		}
		left, ok := evalString(value.X, consts)
		if !ok {
			return "", false
		}
		right, ok := evalString(value.Y, consts)
		if !ok {
			return "", false
		}
		return left + right, true
	case *ast.ParenExpr:
		return evalString(value.X, consts)
	}
	return "", false
}

func stringsInExpr(expr ast.Expr, consts map[string]string) []string {
	var out []string
	ast.Inspect(expr, func(n ast.Node) bool {
		node, ok := n.(ast.Expr)
		if !ok {
			return true
		}
		if text, ok := evalString(node, consts); ok {
			out = append(out, text)
			return false
		}
		return true
	})
	return out
}

func envCallArgs(name string, call *ast.CallExpr) []ast.Expr {
	switch name {
	case "Get":
		return call.Args
	case "GetWithFallback":
		return call.Args
	case "GetOrFile", "GetOrDefaultString", "GetOrDefaultBool", "GetOrDefaultInt", "GetOrDefaultSecond", "GetOneWithFallback":
		if len(call.Args) > 0 {
			return call.Args[:1]
		}
	}
	return nil
}

func validEnv(value string) bool {
	if value == "" {
		return false
	}
	hasLetter := false
	hasUnderscore := false
	for _, r := range value {
		if r >= 'A' && r <= 'Z' {
			hasLetter = true
		}
		if r == '_' {
			hasUnderscore = true
		}
		if !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '_' {
			return false
		}
	}
	if !hasLetter || !hasUnderscore {
		return false
	}
	if strings.HasPrefix(value, "LEGO_") {
		return false
	}
	for _, suffix := range tuningSuffixes {
		if strings.HasSuffix(value, suffix) {
			return false
		}
	}
	return true
}

func commonNamespace(envs []string) string {
	if len(envs) == 1 {
		env := envs[0]
		if index := strings.Index(env, "_"); index > 0 {
			suffix := env[index+1:]
			if _, ok := labelKeyBySuffix[suffix]; ok {
				return env[:index+1]
			}
		}
		return ""
	}
	prefix := envs[0]
	for _, env := range envs[1:] {
		for !strings.HasPrefix(env, prefix) {
			if prefix == "" {
				return ""
			}
			prefix = prefix[:len(prefix)-1]
		}
	}
	if index := strings.LastIndex(prefix, "_"); index >= 0 {
		return prefix[:index+1]
	}
	return ""
}

func humanize(value string) string {
	words := strings.Split(value, "_")
	known := map[string]string{
		"API": "API", "ID": "ID", "URL": "URL", "URI": "URI", "IP": "IP",
		"DNS": "DNS", "HTTP": "HTTP", "HTTPS": "HTTPS", "TLS": "TLS", "TTL": "TTL",
		"SSH": "SSH", "JSON": "JSON", "OAUTH": "OAuth", "PEM": "PEM", "CNAME": "CNAME", "OS": "OS",
	}
	out := make([]string, 0, len(words))
	for _, word := range words {
		if word == "" {
			continue
		}
		if replacement, ok := known[word]; ok {
			out = append(out, replacement)
			continue
		}
		lower := strings.ToLower(word)
		runes := []rune(lower)
		runes[0] = unicode.ToUpper(runes[0])
		out = append(out, string(runes))
	}
	return strings.Join(out, " ")
}

func isTextarea(value string) bool {
	return strings.Contains(value, "JSON") || strings.Contains(value, "SERVICE_ACCOUNT") || strings.Contains(value, "CREDENTIALS")
}

func isSecret(value string) bool {
	if strings.HasSuffix(value, "_ID") || strings.HasSuffix(value, "_NAME") ||
		strings.Contains(value, "USERNAME") || strings.Contains(value, "EMAIL") ||
		strings.Contains(value, "URL") || strings.Contains(value, "ENDPOINT") || strings.Contains(value, "REGION") ||
		strings.Contains(value, "ZONE") || strings.Contains(value, "PROJECT") || strings.Contains(value, "PATH") {
		return false
	}
	for _, token := range []string{"SECRET", "TOKEN", "PASSWORD", "PASS", "PRIVATE_KEY", "KEY", "CREDENTIAL"} {
		if strings.Contains(value, token) {
			return true
		}
	}
	return false
}

func writeGenerated(path string, providers []providerDef) {
	var buffer bytes.Buffer
	buffer.WriteString("// Code generated by scripts/gen_dns_providers; DO NOT EDIT.\n\n")
	buffer.WriteString("package service\n\n")
	buffer.WriteString("var generatedDNSProviders = []ProviderDef{\n")
	for _, provider := range providers {
		buffer.WriteString("\t{\n")
		fmt.Fprintf(&buffer, "\t\tValue:    %q,\n", provider.Value)
		fmt.Fprintf(&buffer, "\t\tLabelKey: %q,\n", "meta.dns."+provider.Value+".label")
		fmt.Fprintf(&buffer, "\t\tDescKey:  %q,\n", "meta.dns."+provider.Value+".desc")
		if len(provider.Fields) == 0 {
			buffer.WriteString("\t},\n")
			continue
		}
		buffer.WriteString("\t\tFields: []FieldDef{\n")
		for _, field := range provider.Fields {
			buffer.WriteString("\t\t\t{")
			fmt.Fprintf(&buffer, "Key: %q, ", field.Key)
			if field.LabelKey != "" {
				fmt.Fprintf(&buffer, "LabelKey: %q, ", field.LabelKey)
			} else {
				fmt.Fprintf(&buffer, "Label: %q, ", field.Label)
			}
			fmt.Fprintf(&buffer, "Type: %q, ", field.Type)
			if field.Required {
				buffer.WriteString("Required: true, ")
			}
			if field.Secret {
				buffer.WriteString("Secret: true, ")
			}
			fmt.Fprintf(&buffer, "Env: %q},\n", field.Env)
		}
		buffer.WriteString("\t\t},\n")
		buffer.WriteString("\t},\n")
	}
	buffer.WriteString("}\n")
	check(os.WriteFile(path, buffer.Bytes(), 0o644))
}

func updateLocale(path string, providers []providerDef, chinese bool) {
	raw, err := os.ReadFile(path)
	check(err)
	lines := strings.Split(string(raw), "\n")

	type entry struct{ key, value string }
	entries := make([]entry, 0, len(providers)*2)
	for _, provider := range providers {
		desc := "Manage DNS records through the " + provider.Name + " API."
		if chinese {
			desc = "通过 " + provider.Name + " 接口管理 DNS 记录。"
		}
		entries = append(entries,
			entry{"meta.dns." + provider.Value + ".label", provider.Name},
			entry{"meta.dns." + provider.Value + ".desc", desc},
		)
	}

	// 已存在的生成条目直接替换，便于重跑生成器时更新展示名。
	pending := make([]entry, 0, len(entries))
	for _, item := range entries {
		key, _ := json.Marshal(item.key)
		value, _ := json.Marshal(item.value)
		line := "  " + string(key) + ": " + string(value) + ","
		found := false
		for i, existing := range lines {
			if strings.HasPrefix(existing, "  "+string(key)+":") {
				lines[i] = line
				found = true
				break
			}
		}
		if !found {
			pending = append(pending, item)
		}
	}
	if len(pending) == 0 {
		check(os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644))
		return
	}

	insertAt := -1
	for i, line := range lines {
		if strings.HasPrefix(line, `  "meta.dns.`) {
			insertAt = i
		}
	}
	if insertAt < 0 {
		insertAt = len(lines) - 2
	}
	if !strings.HasSuffix(strings.TrimSpace(lines[insertAt]), ",") {
		lines[insertAt] = strings.TrimRight(lines[insertAt], " ") + ","
	}

	var block []string
	for _, item := range pending {
		key, _ := json.Marshal(item.key)
		value, _ := json.Marshal(item.value)
		block = append(block, "  "+string(key)+": "+string(value)+",")
	}
	lines = append(lines[:insertAt+1], append(block, lines[insertAt+1:]...)...)
	check(os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644))
}

func countFields(providers []providerDef) int {
	total := 0
	for _, provider := range providers {
		total += len(provider.Fields)
	}
	return total
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
