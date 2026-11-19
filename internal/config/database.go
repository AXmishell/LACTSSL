package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// RewriteDatabase 原子改写配置文件中的 database 段，保留其他字段与注释。
// 返回旧配置备份路径。调用方应先停止写入并完成目标库校验。
func RewriteDatabase(path, driver, dsn, sqlitePath string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("配置文件路径不能为空")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取配置文件失败: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("解析配置文件失败: %w", err)
	}
	root, err := documentMapping(&doc)
	if err != nil {
		return "", err
	}
	dbNode, err := ensureMapping(root, "database")
	if err != nil {
		return "", err
	}
	values := []struct {
		key   string
		value string
	}{
		{"driver", driver},
		{"path", sqlitePath},
		{"dsn", dsn},
	}
	for _, item := range values {
		if err := setScalar(dbNode, item.key, "!!str", item.value); err != nil {
			return "", err
		}
	}

	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(&doc); err != nil {
		return "", fmt.Errorf("序列化配置失败: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return "", fmt.Errorf("序列化配置失败: %w", err)
	}
	fc := defaults()
	if err := yaml.Unmarshal(buf.Bytes(), fc); err != nil {
		return "", fmt.Errorf("校验新配置失败: %w", err)
	}
	if err := validateFileValues(fc); err != nil {
		return "", fmt.Errorf("校验新配置失败: %w", err)
	}
	backup := path + ".before-db-convert-" + time.Now().UTC().Format("20060102-150405")
	if err := os.WriteFile(backup, raw, 0o600); err != nil {
		return "", fmt.Errorf("备份配置文件失败: %w", err)
	}
	if err := writeFileAtomic(path, buf.Bytes()); err != nil {
		return backup, err
	}
	return backup, nil
}
