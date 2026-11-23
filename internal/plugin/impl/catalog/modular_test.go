package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// 校验单 provider 源文件与生成的 data.json 保持一致。
// 手改 data.json 或忘记运行 make catalog 时，这个测试会失败。
func TestModularSourceFilesMatchAggregate(t *testing.T) {
	for _, tc := range []struct {
		kind string
		list []Provider
	}{
		{"dns", DNSProviders()},
		{"deploy", Deployers()},
	} {
		entries, err := os.ReadDir(tc.kind)
		if err != nil {
			t.Fatalf("read catalog/%s: %v", tc.kind, err)
		}
		if len(entries) != len(tc.list) {
			t.Fatalf("catalog/%s files = %d, aggregate = %d; run make catalog", tc.kind, len(entries), len(tc.list))
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(tc.kind, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			var want Provider
			if err := json.Unmarshal(raw, &want); err != nil {
				t.Fatalf("%s/%s: %v", tc.kind, entry.Name(), err)
			}
			got, ok := lookup(tc.list, want.Value)
			if !ok {
				t.Fatalf("catalog/%s/%s: %q missing from aggregate", tc.kind, entry.Name(), want.Value)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("catalog/%s/%s differs from aggregate; run make catalog", tc.kind, entry.Name())
			}
		}
	}
}
