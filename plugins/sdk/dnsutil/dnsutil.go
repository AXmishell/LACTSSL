// Package dnsutil 提供 DNS-01 provider 共用的域名与 TXT 处理函数。
package dnsutil

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// cnameResolver 由 helper 启动时注入；默认关闭，避免单元测试访问真实 DNS。
var cnameResolver struct {
	sync.RWMutex
	fn func(string) (string, bool)
}

// SetCNAMEResolver 替换 CNAME 解析器，主要用于测试。
func SetCNAMEResolver(fn func(string) (string, bool)) {
	cnameResolver.Lock()
	defer cnameResolver.Unlock()
	cnameResolver.fn = fn
}

// EnableCNAMEResolution 让 ChallengeFQDN 跟随 _acme-challenge 的 CNAME。
// lego v4/v5 默认会跟随 CNAME，native provider 需要保持同样的行为。
func EnableCNAMEResolution() {
	if cnameDisabledByEnv() {
		SetCNAMEResolver(nil)
		return
	}
	resolver := &net.Resolver{PreferGo: true}
	var mu sync.Mutex
	cache := map[string]string{}
	SetCNAMEResolver(func(fqdn string) (string, bool) {
		key := strings.ToLower(strings.TrimSpace(fqdn))
		if key == "" {
			return "", false
		}
		mu.Lock()
		target, ok := cache[key]
		mu.Unlock()
		if ok {
			return target, target != ""
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		name, err := resolver.LookupCNAME(ctx, strings.TrimSuffix(fqdn, "."))
		if err != nil {
			mu.Lock()
			cache[key] = ""
			mu.Unlock()
			return "", false
		}
		name = Fqdn(name)
		if strings.EqualFold(name, Fqdn(fqdn)) {
			name = ""
		}
		mu.Lock()
		cache[key] = name
		mu.Unlock()
		return name, name != ""
	})
}

func cnameDisabledByEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LEGO_DISABLE_CNAME_SUPPORT"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// Fqdn 保证域名以点结尾。
func Fqdn(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if strings.HasSuffix(name, ".") {
		return name
	}
	return name + "."
}

// UnFqdn 去掉结尾的点。
func UnFqdn(name string) string {
	return strings.TrimSuffix(strings.TrimSpace(name), ".")
}

// ChallengeFQDN 返回 _acme-challenge.<domain>.。
func ChallengeFQDN(domain string) string {
	domain = UnFqdn(domain)
	if !strings.HasPrefix(domain, "_acme-challenge.") {
		domain = "_acme-challenge." + domain
	}
	fqdn := Fqdn(domain)
	cnameResolver.RLock()
	resolve := cnameResolver.fn
	cnameResolver.RUnlock()
	if resolve != nil {
		if target, ok := resolve(fqdn); ok && strings.TrimSpace(target) != "" {
			return Fqdn(target)
		}
	}
	return fqdn
}

// ZoneName 从候选 zone 列表中选出最长的后缀匹配。
// 传入的 zone 可以带或不带结尾点。
func ZoneName(fqdn string, zones []string) (string, error) {
	name := strings.ToLower(Fqdn(fqdn))
	best := ""
	for _, zone := range zones {
		zone = strings.ToLower(Fqdn(zone))
		if zone == "." || zone == "" {
			continue
		}
		if name == zone || strings.HasSuffix(name, "."+zone) {
			if len(zone) > len(best) {
				best = zone
			}
		}
	}
	if best == "" {
		return "", fmt.Errorf("no matching DNS zone for %s", fqdn)
	}
	return best, nil
}

// RelativeName 返回 fqdn 相对于 zone 的记录名。
// 例如 _acme-challenge.example.com. 在 example.com. 下返回 _acme-challenge。
func RelativeName(fqdn, zone string) (string, error) {
	name := strings.ToLower(Fqdn(fqdn))
	zone = strings.ToLower(Fqdn(zone))
	if name == zone {
		return "@", nil
	}
	suffix := "." + zone
	if !strings.HasSuffix(name, suffix) {
		return "", fmt.Errorf("%s is not inside zone %s", fqdn, zone)
	}
	return strings.TrimSuffix(name, suffix), nil
}

// TXTValue 返回 DNS-01 挑战的 TXT 记录值：base64url(sha256(keyAuth))，无 padding。
// keyAuth 是 ACME 的 key authorization（token.thumbprint），不能直接写入 DNS。
func TXTValue(keyAuth string) string {
	sum := sha256.Sum256([]byte(keyAuth))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// QuoteTXT 返回带双引号的 TXT 值。
func QuoteTXT(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
		return value
	}
	return `"` + value + `"`
}

// UnquoteTXT 去掉 TXT 值两侧的引号。
func UnquoteTXT(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
		return value[1 : len(value)-1]
	}
	return value
}

// ContainsTXT 判断 TXT 值列表是否包含目标值（忽略引号与大小写）。
func ContainsTXT(values []string, target string) bool {
	target = UnquoteTXT(target)
	for _, value := range values {
		// TXT RDATA 区分大小写；ACME 值是 base64url。
		if UnquoteTXT(value) == target {
			return true
		}
	}
	return false
}

// RemoveTXT 返回删除目标值后的列表。
func RemoveTXT(values []string, target string) []string {
	target = UnquoteTXT(target)
	out := make([]string, 0, len(values))
	for _, value := range values {
		if UnquoteTXT(value) == target {
			continue
		}
		out = append(out, value)
	}
	return out
}

// SortStrings 返回排序后的副本。
func SortStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}
