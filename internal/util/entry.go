package util

import (
	"crypto/rand"
	"regexp"

	"lactssl/internal/i18n"
	"strings"
)

// entryPattern 限制安全入口为 8-32 位字母、数字、下划线和中划线。
// 手工入口不应短于 8 位，避免被分布式扫描快速枚举。
var entryPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{8,32}$`)

// reservedEntries 是不能用作安全入口的名称，避免与接口或静态资源前缀冲突。
var reservedEntries = map[string]struct{}{
	"api":     {},
	"assets":  {},
	"healthz": {},
	"static":  {},
	"login":   {},
	"install": {},
	"reset":   {},
	"acme":    {},
	"pki":     {},
}

// ValidateEntry 规范化并校验安全入口，允许首尾带斜杠的写法。
func ValidateEntry(raw string) (string, error) {
	entry := strings.Trim(strings.TrimSpace(raw), "/")
	if !entryPattern.MatchString(entry) {
		return "", i18n.E("api.error.entry_format")
	}
	if _, reserved := reservedEntries[strings.ToLower(entry)]; reserved {
		return "", i18n.E("api.error.entry_reserved", "entry", entry)
	}
	return entry, nil
}

// entryAlphabet 是安全入口可用的字符集：大小写字母和数字。
const entryAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// entryLength 是随机安全入口的固定长度。
const entryLength = 20

// GenerateEntry 生成 20 位由大小写字母和数字组成的随机安全入口。
func GenerateEntry() (string, error) {
	out := make([]byte, 0, entryLength)
	buf := make([]byte, entryLength*2)
	for len(out) < entryLength {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			// 248 = 4 * 62，丢弃 >= 248 的字节以消除取模偏差。
			if int(b) >= 256-256%len(entryAlphabet) {
				continue
			}
			out = append(out, entryAlphabet[int(b)%len(entryAlphabet)])
			if len(out) == entryLength {
				break
			}
		}
	}
	return string(out), nil
}
