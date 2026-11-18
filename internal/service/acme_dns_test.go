package service

import (
	"testing"
)

// TestBuiltinDNSProviderIDsResolve 验证历史默认 DNS 服务商仍能从 manifest 缓存
// 解析出元数据。宿主不再内置 catalog，缓存由插件发现/安装流程建立。
func TestBuiltinDNSProviderIDsResolve(t *testing.T) {
	ids := BuiltinDNSProviderIDs()
	if len(ids) != 35 {
		t.Fatalf("builtin DNS providers = %d, want 35", len(ids))
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Errorf("duplicate default provider %s", id)
		}
		seen[id] = true
		if _, ok := DNSProviderInfo(id); !ok {
			t.Errorf("default provider %s missing from manifest cache", id)
		}
	}
}
