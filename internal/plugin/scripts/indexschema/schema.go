// Package indexschema 校验插件仓库 index.json 的结构契约。
//
// 这里实现 JSON Schema 中索引实际用到的子集：type/required/properties/
// additionalProperties/items/enum/const/pattern/min*/max*/uniqueItems/$ref/
// format(date-time)。校验器不依赖第三方包，发布流程与单元测试共用同一份 schema。
package indexschema

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

//go:embed index.schema.json
var SchemaJSON []byte

// Validate 使用内置 schema 校验索引文档。
func Validate(document []byte) error {
	return ValidateWithSchema(SchemaJSON, document)
}

// ValidateWithSchema 使用指定 schema 校验文档，便于测试与第三方工具复用。
func ValidateWithSchema(schemaJSON, document []byte) error {
	var schema any
	if err := json.Unmarshal(schemaJSON, &schema); err != nil {
		return fmt.Errorf("解析 JSON Schema 失败: %w", err)
	}
	root, ok := schema.(map[string]any)
	if !ok {
		return fmt.Errorf("JSON Schema 根节点必须是对象")
	}
	var value any
	if err := json.Unmarshal(document, &value); err != nil {
		return fmt.Errorf("解析 JSON 文档失败: %w", err)
	}
	return validateNode(root, root, value, "$", 0)
}

func validateNode(schema, root map[string]any, value any, path string, depth int) error {
	if depth > 64 {
		return fmt.Errorf("%s: schema 引用层级过深", path)
	}
	if ref, ok := schema["$ref"].(string); ok {
		target, err := resolveRef(root, ref)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if err := validateNode(target, root, value, path, depth+1); err != nil {
			return err
		}
	}
	if rawType, ok := schema["type"]; ok && !matchesType(rawType, value) {
		return fmt.Errorf("%s: 类型应为 %v，实际为 %s", path, rawType, typeName(value))
	}
	if constant, ok := schema["const"]; ok && !reflect.DeepEqual(constant, value) {
		return fmt.Errorf("%s: 应等于 %v，实际为 %v", path, constant, value)
	}
	if enum, ok := schema["enum"].([]any); ok {
		matched := false
		for _, item := range enum {
			if reflect.DeepEqual(item, value) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s: 值 %v 不在允许的枚举中", path, value)
		}
	}

	switch typed := value.(type) {
	case map[string]any:
		if err := validateObject(schema, root, typed, path, depth); err != nil {
			return err
		}
	case []any:
		if err := validateArray(schema, root, typed, path, depth); err != nil {
			return err
		}
	case string:
		if err := validateString(schema, typed, path); err != nil {
			return err
		}
	case float64:
		if min, ok := numberValue(schema["minimum"]); ok && typed < min {
			return fmt.Errorf("%s: 值 %v 小于最小值 %v", path, typed, min)
		}
		if max, ok := numberValue(schema["maximum"]); ok && typed > max {
			return fmt.Errorf("%s: 值 %v 大于最大值 %v", path, typed, max)
		}
	}
	return nil
}

func validateObject(schema, root map[string]any, object map[string]any, path string, depth int) error {
	if required, ok := schema["required"].([]any); ok {
		for _, raw := range required {
			name, _ := raw.(string)
			if _, found := object[name]; !found {
				return fmt.Errorf("%s: 缺少必填字段 %q", path, name)
			}
		}
	}
	if min, ok := integerValue(schema["minProperties"]); ok && len(object) < min {
		return fmt.Errorf("%s: 属性数量 %d 小于 %d", path, len(object), min)
	}
	properties, _ := schema["properties"].(map[string]any)
	for key, value := range object {
		if childSchema, ok := properties[key].(map[string]any); ok {
			if err := validateNode(childSchema, root, value, path+"."+key, depth+1); err != nil {
				return err
			}
			continue
		}
		switch additional := schema["additionalProperties"].(type) {
		case bool:
			if !additional {
				return fmt.Errorf("%s: 不允许的字段 %q", path, key)
			}
		case map[string]any:
			if err := validateNode(additional, root, value, path+"."+key, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateArray(schema, root map[string]any, array []any, path string, depth int) error {
	if min, ok := integerValue(schema["minItems"]); ok && len(array) < min {
		return fmt.Errorf("%s: 数组长度 %d 小于 %d", path, len(array), min)
	}
	if max, ok := integerValue(schema["maxItems"]); ok && len(array) > max {
		return fmt.Errorf("%s: 数组长度 %d 大于 %d", path, len(array), max)
	}
	if unique, _ := schema["uniqueItems"].(bool); unique {
		seen := make(map[string]bool, len(array))
		for index, item := range array {
			key := uniqueKey(item)
			if seen[key] {
				return fmt.Errorf("%s: 数组元素重复（下标 %d）", path, index)
			}
			seen[key] = true
		}
	}
	if itemSchema, ok := schema["items"].(map[string]any); ok {
		for index, item := range array {
			if err := validateNode(itemSchema, root, item, fmt.Sprintf("%s[%d]", path, index), depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateString(schema map[string]any, value, path string) error {
	length := utf8.RuneCountInString(value)
	if min, ok := integerValue(schema["minLength"]); ok && length < min {
		return fmt.Errorf("%s: 长度 %d 小于 %d", path, length, min)
	}
	if max, ok := integerValue(schema["maxLength"]); ok && length > max {
		return fmt.Errorf("%s: 长度 %d 大于 %d", path, length, max)
	}
	if pattern, ok := schema["pattern"].(string); ok {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return fmt.Errorf("%s: schema pattern 非法: %w", path, err)
		}
		if !compiled.MatchString(value) {
			return fmt.Errorf("%s: 值 %q 不匹配 %q", path, value, pattern)
		}
	}
	if format, _ := schema["format"].(string); format == "date-time" {
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			return fmt.Errorf("%s: 不是有效的 RFC3339 时间: %q", path, value)
		}
	}
	return nil
}

func resolveRef(root map[string]any, ref string) (map[string]any, error) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("不支持外部 $ref: %s", ref)
	}
	current := any(root)
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		object, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("$ref 路径不存在: %s", ref)
		}
		current, ok = object[part]
		if !ok {
			return nil, fmt.Errorf("$ref 路径不存在: %s", ref)
		}
	}
	target, ok := current.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("$ref 目标不是对象: %s", ref)
	}
	return target, nil
}

func matchesType(rawType any, value any) bool {
	switch typed := rawType.(type) {
	case string:
		return matchesSingleType(typed, value)
	case []any:
		for _, item := range typed {
			if name, ok := item.(string); ok && matchesSingleType(name, value) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func matchesSingleType(name string, value any) bool {
	switch name {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		number, ok := value.(float64)
		return ok && math.Trunc(number) == number
	case "null":
		return value == nil
	default:
		return false
	}
}

func typeName(value any) string {
	switch value.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%T", value)
	}
}

func numberValue(value any) (float64, bool) {
	number, ok := value.(float64)
	return number, ok
}

func integerValue(value any) (int, bool) {
	number, ok := value.(float64)
	if !ok || math.Trunc(number) != number {
		return 0, false
	}
	return int(number), true
}

func uniqueKey(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%#v", value)
	}
	return string(raw)
}
