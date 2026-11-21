package netutil

import (
	"net/http"
	"net/netip"
	"strings"
	"testing"
)

func mustTrusted(t *testing.T, values ...string) TrustedSet {
	t.Helper()
	set, err := CompileTrusted(values)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func TestParsePeer(t *testing.T) {
	tests := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"192.0.2.1:1234", "192.0.2.1", true},
		{"192.0.2.1", "192.0.2.1", true},
		{"[2001:db8::1]:443", "2001:db8::1", true},
		{"2001:db8::1", "2001:db8::1", true},
		{"::ffff:192.0.2.1", "192.0.2.1", true},
		{"fe80::1%eth0", "fe80::1", true},
		{"", "", false},
		{"not-an-ip:1234", "", false},
	}
	for _, test := range tests {
		got, ok := ParsePeer(test.raw)
		if ok != test.ok {
			t.Fatalf("ParsePeer(%q) ok=%v want %v", test.raw, ok, test.ok)
		}
		if ok && got.String() != test.want {
			t.Fatalf("ParsePeer(%q)=%s want %s", test.raw, got, test.want)
		}
	}
}

func TestParseHeaderIP(t *testing.T) {
	tests := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"203.0.113.9", "203.0.113.9", true},
		{" 203.0.113.9 ", "203.0.113.9", true},
		{"[2001:db8::1]", "2001:db8::1", true},
		{"::ffff:203.0.113.9", "203.0.113.9", true},
		{"2001:db8::1:443", "2001:db8::1:443", true},
		{"203.0.113.9:443", "", false},
		{"[2001:db8::1]:443", "", false},
		{"fe80::1%eth0", "", false},
		{"unknown", "", false},
		{"", "", false},
	}
	for _, test := range tests {
		got, ok := ParseHeaderIP(test.raw)
		if ok != test.ok {
			t.Fatalf("ParseHeaderIP(%q) ok=%v want %v", test.raw, ok, test.ok)
		}
		if ok && got.String() != test.want {
			t.Fatalf("ParseHeaderIP(%q)=%s want %s", test.raw, got, test.want)
		}
	}
}

func TestCompileTrusted(t *testing.T) {
	set := mustTrusted(t, " 127.0.0.1 ", "::ffff:192.0.2.1", "10.1.2.3/8", "::ffff:172.16.0.0/108")
	for _, raw := range []string{"127.0.0.1", "192.0.2.1", "10.1.2.3", "172.16.1.1"} {
		if !set.Contains(netip.MustParseAddr(raw)) {
			t.Fatalf("可信集合应包含 %s", raw)
		}
	}
	if set.Contains(netip.MustParseAddr("203.0.113.1")) {
		t.Fatal("可信集合不应包含未配置地址")
	}
	if !set.Contains(netip.MustParseAddr("::ffff:127.0.0.1")) {
		t.Fatal("Contains 应先 Unmap 再比较")
	}

	for _, invalid := range []string{"0.0.0.0/0", "::/0", "not-an-ip", "10.0.0.0/33", "fe80::1%eth0"} {
		if _, err := CompileTrusted([]string{invalid}); err == nil {
			t.Fatalf("应拒绝非法可信代理 %q", invalid)
		}
	}
}

func TestResolve(t *testing.T) {
	trusted := mustTrusted(t, "10.0.0.2", "10.0.0.3")
	tests := []struct {
		name       string
		remote     string
		headers    map[string][]string
		trusted    TrustedSet
		wantIP     string
		wantSource Source
		wantSkip   int
	}{
		{
			name:       "空可信列表忽略转发头",
			remote:     "198.51.100.1:1234",
			headers:    map[string][]string{"X-Forwarded-For": {"203.0.113.9"}},
			trusted:    TrustedSet{},
			wantIP:     "198.51.100.1",
			wantSource: SourceRemote,
		},
		{
			name:       "可信代理单值 XFF",
			remote:     "10.0.0.2:1234",
			headers:    map[string][]string{"X-Forwarded-For": {"203.0.113.9"}},
			trusted:    trusted,
			wantIP:     "203.0.113.9",
			wantSource: SourceXForwardedFor,
		},
		{
			name:       "多级 XFF 跳过可信中间跳",
			remote:     "10.0.0.3:1234",
			headers:    map[string][]string{"X-Forwarded-For": {"203.0.113.9, 10.0.0.2"}},
			trusted:    trusted,
			wantIP:     "203.0.113.9",
			wantSource: SourceXForwardedFor,
			wantSkip:   1,
		},
		{
			name:       "XFF 中间跳不可信则停在它",
			remote:     "10.0.0.2:1234",
			headers:    map[string][]string{"X-Forwarded-For": {"203.0.113.9, 198.51.100.7"}},
			trusted:    trusted,
			wantIP:     "198.51.100.7",
			wantSource: SourceXForwardedFor,
		},
		{
			name:       "全链可信回退 peer",
			remote:     "10.0.0.2:1234",
			headers:    map[string][]string{"X-Forwarded-For": {"10.0.0.3, 10.0.0.2"}},
			trusted:    trusted,
			wantIP:     "10.0.0.2",
			wantSource: SourceRemote,
		},
		{
			name:       "全链可信时使用 X-Real-IP",
			remote:     "10.0.0.2:1234",
			headers:    map[string][]string{"X-Forwarded-For": {"10.0.0.3"}, "X-Real-Ip": {"203.0.113.9"}},
			trusted:    trusted,
			wantIP:     "203.0.113.9",
			wantSource: SourceXRealIP,
		},
		{
			name:       "XFF 非法项回退 X-Real-IP",
			remote:     "10.0.0.2:1234",
			headers:    map[string][]string{"X-Forwarded-For": {"203.0.113.9, bad"}, "X-Real-Ip": {"203.0.113.10"}},
			trusted:    trusted,
			wantIP:     "203.0.113.10",
			wantSource: SourceXRealIP,
		},
		{
			name:       "XFF 与 X-Real-IP 均不可用回退 peer",
			remote:     "10.0.0.2:1234",
			headers:    map[string][]string{"X-Forwarded-For": {"203.0.113.9, bad"}},
			trusted:    trusted,
			wantIP:     "10.0.0.2",
			wantSource: SourceRemote,
		},
		{
			name:       "仅 X-Real-IP",
			remote:     "10.0.0.2:1234",
			headers:    map[string][]string{"X-Real-Ip": {"203.0.113.9"}},
			trusted:    trusted,
			wantIP:     "203.0.113.9",
			wantSource: SourceXRealIP,
		},
		{
			name:       "X-Real-IP 是可信代理自身",
			remote:     "10.0.0.2:1234",
			headers:    map[string][]string{"X-Real-Ip": {"10.0.0.2"}},
			trusted:    trusted,
			wantIP:     "10.0.0.2",
			wantSource: SourceRemote,
		},
		{
			name:       "多行 XFF 合并",
			remote:     "10.0.0.2:1234",
			headers:    map[string][]string{"X-Forwarded-For": {"203.0.113.9", "198.51.100.7"}},
			trusted:    trusted,
			wantIP:     "198.51.100.7",
			wantSource: SourceXForwardedFor,
		},
		{
			name:       "无端口 RemoteAddr 兼容",
			remote:     "10.0.0.2",
			headers:    map[string][]string{"X-Forwarded-For": {"203.0.113.9"}},
			trusted:    trusted,
			wantIP:     "203.0.113.9",
			wantSource: SourceXForwardedFor,
		},
		{
			name:       "RemoteAddr 非法",
			remote:     "not-an-ip",
			trusted:    trusted,
			wantSource: SourceRemote,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := Resolve(test.remote, http.Header(test.headers), test.trusted)
			if test.wantIP == "" {
				if result.IP.IsValid() {
					t.Fatalf("IP=%s want invalid (warnings=%v)", result.IP, result.Warnings)
				}
			} else if result.IP.String() != test.wantIP {
				t.Fatalf("IP=%s want %s (warnings=%v)", result.IP, test.wantIP, result.Warnings)
			}
			if result.Source != test.wantSource {
				t.Fatalf("Source=%s want %s", result.Source, test.wantSource)
			}
			if result.Skipped != test.wantSkip {
				t.Fatalf("Skipped=%d want %d", result.Skipped, test.wantSkip)
			}
		})
	}
}

func TestResolveForwarded(t *testing.T) {
	trusted := mustTrusted(t, "10.0.0.2", "10.0.0.3")
	tests := []struct {
		name      string
		forwarded string
		wantIP    string
		wantSkip  int
	}{
		{"单值", "for=203.0.113.9;proto=https", "203.0.113.9", 0},
		{"多级链跳过可信项", "for=203.0.113.9, for=10.0.0.3;proto=https", "203.0.113.9", 1},
		{"带端口", "for=203.0.113.9:4711", "203.0.113.9", 0},
		{"带引号 IPv6 与端口", `for="[2001:db8::1]:4711"`, "2001:db8::1", 0},
		{"带方括号 IPv6", `for="[2001:db8::1]"`, "2001:db8::1", 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := ResolveWithOptions("10.0.0.2:1234",
				http.Header{"Forwarded": {test.forwarded}},
				trusted, Options{EnableForwarded: true})
			if result.IP.String() != test.wantIP || result.Source != SourceForwarded || result.Skipped != test.wantSkip {
				t.Fatalf("unexpected result: %+v", result)
			}
		})
	}

	for _, raw := range []string{"for=unknown", "for=_hidden", "for=not-an-ip", "proto=https"} {
		result := ResolveWithOptions("10.0.0.2:1234",
			http.Header{"Forwarded": {raw}}, trusted, Options{EnableForwarded: true})
		if result.IP.String() != "10.0.0.2" || result.Source != SourceRemote {
			t.Fatalf("非法 Forwarded 应回退 peer: %q -> %+v", raw, result)
		}
	}
}

func TestResolveCloudflareAndPeerVerified(t *testing.T) {
	trusted := mustTrusted(t, "198.51.100.0/24")

	cloudflare := ResolveWithOptions("198.51.100.10:1234",
		http.Header{
			"Cf-Connecting-Ip": {"203.0.113.9"},
			"X-Forwarded-For":  {"203.0.113.10"},
		},
		trusted, Options{Cloudflare: true})
	if cloudflare.IP.String() != "203.0.113.9" || cloudflare.Source != SourceCFConnectingIP {
		t.Fatalf("Cloudflare 模式应优先 CF-Connecting-IP: %+v", cloudflare)
	}

	untrusted := ResolveWithOptions("203.0.113.1:1234",
		http.Header{"Cf-Connecting-Ip": {"203.0.113.9"}},
		trusted, Options{Cloudflare: true})
	if untrusted.IP.String() != "203.0.113.1" || untrusted.Source != SourceRemote {
		t.Fatalf("非可信 peer 不应采信 CF 头: %+v", untrusted)
	}

	verified := ResolveWithOptions("203.0.113.1:1234",
		http.Header{"X-Forwarded-For": {"203.0.113.9"}},
		TrustedSet{}, Options{PeerVerified: true})
	if verified.IP.String() != "203.0.113.9" || verified.Source != SourceXForwardedFor {
		t.Fatalf("mTLS 验证后应按可信代理解析: %+v", verified)
	}
}

func TestResolveLimits(t *testing.T) {
	trusted := mustTrusted(t, "10.0.0.2")
	header := http.Header{"X-Forwarded-For": {strings.Join([]string{
		"203.0.113.1", "203.0.113.2", "203.0.113.3",
	}, ",")}}
	result := ResolveWithOptions("10.0.0.2:1234", header, trusted, Options{MaxHops: 2, MaxHeaderBytes: 1024})
	if result.IP.String() != "10.0.0.2" || result.Source != SourceRemote {
		t.Fatalf("超过跳数上限应回退 peer: %+v", result)
	}

	long := "203.0.113.9"
	result = ResolveWithOptions("10.0.0.2:1234",
		http.Header{"X-Forwarded-For": {long + strings.Repeat("0", 64)}},
		trusted, Options{MaxHops: 32, MaxHeaderBytes: 8})
	if result.IP.String() != "10.0.0.2" || result.Source != SourceRemote {
		t.Fatalf("超过长度上限应回退 peer: %+v", result)
	}
}
