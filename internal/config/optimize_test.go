package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// collectConfigYAMLPaths 收集 fileConfig 中定义的全部 YAML 叶子字段路径。
func collectConfigYAMLPaths(t reflect.Type, prefix string, out map[string]bool) {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := strings.Split(field.Tag.Get("yaml"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		path := tag
		if prefix != "" {
			path = prefix + "." + tag
		}
		typ := field.Type
		for typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		if typ.Kind() == reflect.Struct {
			collectConfigYAMLPaths(typ, path, out)
			continue
		}
		out[path] = true
	}
}

// collectSpecPaths 收集 defaultSpec 中的全部叶子字段路径。
func collectSpecPaths(value any, prefix string, out map[string]bool) {
	switch typed := value.(type) {
	case map[string]any:
		// 空映射（例如未填写的 rate_limit.rules）在 fileConfig 中是一个叶子节点。
		if prefix != "" && len(typed) == 0 {
			out[prefix] = true
		}
		for key, child := range typed {
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			collectSpecPaths(child, path, out)
		}
	case []string:
		out[prefix] = true
	default:
		out[prefix] = true
	}
}

// assertYAMLHasAllConfigFields 校验一份 YAML 是否包含 fileConfig 定义的全部叶子字段。
func assertYAMLHasAllConfigFields(t *testing.T, data []byte, label string) {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("%s 不是合法 YAML: %v", label, err)
	}
	present := map[string]bool{}
	collectSpecPaths(doc, "", present)
	// 动态键映射会展开成多条子路径，这里补齐所有父路径，
	// 使 fileConfig 中以整张映射作为叶子的字段也能通过检查。
	for path := range present {
		parts := strings.Split(path, ".")
		for i := 1; i < len(parts); i++ {
			present[strings.Join(parts[:i], ".")] = true
		}
	}

	var missing []string
	for path := range configYAMLFields() {
		if !present[path] {
			missing = append(missing, path)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%s 缺少配置项: %s", label, strings.Join(missing, ", "))
	}
}

func configYAMLFields() map[string]bool {
	paths := map[string]bool{}
	collectConfigYAMLPaths(reflect.TypeOf(fileConfig{}), "", paths)
	return paths
}

// TestDefaultSpecCoversAllConfigFields 防止 fileConfig 新增字段后忘记同步配置优化器，
// 否则旧配置在 /upgrade 中执行“配置优化”时不会补齐这些新字段。
func TestDefaultSpecCoversAllConfigFields(t *testing.T) {
	configPaths := configYAMLFields()

	specPaths := map[string]bool{}
	collectSpecPaths(defaultSpec(), "", specPaths)

	var missing []string
	for path := range configPaths {
		if !specPaths[path] {
			missing = append(missing, path)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("defaultSpec 缺少配置项: %s", strings.Join(missing, ", "))
	}

	var unknown []string
	for path := range specPaths {
		if !configPaths[path] {
			unknown = append(unknown, path)
		}
	}
	if len(unknown) > 0 {
		t.Fatalf("defaultSpec 包含 fileConfig 未定义的配置项: %s", strings.Join(unknown, ", "))
	}
}

func containsChange(report OptimizeReport, key, action string) bool {
	for _, change := range report.Changes {
		if change.Key == key && change.Action == action {
			return true
		}
	}
	return false
}

// TestConfigExampleCoversAllFields 确保仓库根目录的示例配置与当前版本字段保持一致。
func TestConfigExampleCoversAllFields(t *testing.T) {
	path := filepath.Join("..", "..", "config.yaml.example")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertYAMLHasAllConfigFields(t, data, "config.yaml.example")
}

// TestOptimizeFileAddsMissingConfigFields 验证旧配置会补齐会话保持、程序升级公钥与 token，
// 同时保留用户注释和已有密钥。
func TestOptimizeFileAddsMissingConfigFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	secret := strings.Repeat("j", 32)
	salt := strings.Repeat("s", 32)
	original := `# 用户自定义注释，必须保留
data_dir: "` + dir + `"
security:
  # 密钥注释也必须保留
  jwt_secret: "` + secret + `"
  salt: "` + salt + `"
upgrade:
  program:
    repo: "AXmishell/LACTSSL"
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := OptimizeFile(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Applied || !report.Changed {
		t.Fatalf("应写入补齐后的配置: %+v", report)
	}
	for _, key := range []string{
		"security.session_persistence",
		"upgrade.program.public_key",
		"upgrade.program.token",
	} {
		if !containsChange(report, key, "added") {
			t.Fatalf("未补齐配置项 %s: %+v", key, report.Changes)
		}
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	for _, want := range []string{
		"# 用户自定义注释，必须保留",
		"# 密钥注释也必须保留",
		`jwt_secret: "` + secret + `"`,
		`salt: "` + salt + `"`,
		"session_persistence: true",
		`public_key: ""`,
		`token: ""`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("补齐后的配置缺少 %q:\n%s", want, content)
		}
	}
}
