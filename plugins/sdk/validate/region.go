// Package validate 提供 provider 配置值的严格校验。
package validate

import (
	"fmt"
	"regexp"
	"strings"
)

var regionPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// Region 校验云厂商区域/区域 ID，避免把用户输入拼进 URL authority。
func Region(value string) error {
	value = strings.TrimSpace(value)
	if !regionPattern.MatchString(value) {
		return fmt.Errorf("invalid region %q", value)
	}
	return nil
}

// RegionOr 在 value 为空时使用 fallback，并校验结果。
func RegionOr(value, fallback string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	if err := Region(value); err != nil {
		return "", err
	}
	return value, nil
}
