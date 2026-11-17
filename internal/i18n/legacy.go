package i18n

import (
	"regexp"
	"sort"
	"strings"
	"sync"
)

// 历史数据兼容：i18n 改造前的日志、任务消息等直接存的是默认语言文本。
// 这里用默认语言目录反向构建“文本 -> key”的索引，在读取时尽量翻译旧数据。
// 新数据仍然使用 Encode 存储 key + params，不依赖这里的匹配。

type legacyPattern struct {
	key   string
	re    *regexp.Regexp
	names []string
}

var (
	legacyOnce     sync.Once
	legacyExact    map[string]string
	legacyPatterns []legacyPattern
)

// messagePrefixes 限制只对消息类 key 做反向匹配，避免误匹配邮件正文或表单文案。
var messagePrefixes = []string{
	"log.",
	"task.",
	"monitor.",
	"deploy.",
	"cert.issue.",
	"dashboard.bucket.",
}

func isMessageKey(key string) bool {
	for _, prefix := range messagePrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func buildLegacyIndex() {
	legacyOnce.Do(func() {
		load()
		legacyExact = map[string]string{}
		values := catalogs[Default]
		keys := make([]string, 0, len(values))
		for key := range values {
			if isMessageKey(key) {
				keys = append(keys, key)
			}
		}
		// 排序保证相同模板的匹配结果稳定。
		sort.Strings(keys)
		for _, key := range keys {
			text := values[key]
			names := placeholderNames(text)
			if len(names) == 0 {
				if _, exists := legacyExact[text]; !exists {
					legacyExact[text] = key
				}
				continue
			}
			pattern := regexp.QuoteMeta(text)
			valid := true
			for _, name := range names {
				token := regexp.QuoteMeta("{" + name + "}")
				if !strings.Contains(pattern, token) {
					valid = false
					break
				}
				pattern = strings.Replace(pattern, token, "(?P<"+name+">.+?)", 1)
			}
			if !valid {
				continue
			}
			re, err := regexp.Compile("^" + pattern + "$")
			if err != nil {
				continue
			}
			legacyPatterns = append(legacyPatterns, legacyPattern{key: key, re: re, names: names})
		}
	})
}

func placeholderNames(text string) []string {
	matches := placeholderPattern.FindAllStringSubmatch(text, -1)
	names := make([]string, 0, len(matches))
	seen := map[string]bool{}
	for _, match := range matches {
		name := match[1]
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

var placeholderPattern = regexp.MustCompile(`\{([a-zA-Z0-9_]+)\}`)

// matchLegacy 尝试把旧文本匹配到当前目录中的消息模板。
func matchLegacy(text string) (string, map[string]any, bool) {
	buildLegacyIndex()
	if key, ok := legacyExact[text]; ok {
		return key, nil, true
	}
	for _, pattern := range legacyPatterns {
		match := pattern.re.FindStringSubmatch(text)
		if match == nil {
			continue
		}
		params := map[string]any{}
		for index, name := range pattern.re.SubexpNames() {
			if index == 0 || name == "" || index >= len(match) {
				continue
			}
			params[name] = match[index]
		}
		return pattern.key, params, true
	}
	return "", nil, false
}
