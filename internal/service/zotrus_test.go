package service

import (
	"strings"
	"testing"
)

func TestZotrusDNSRecordName(t *testing.T) {
	tests := []struct {
		domain    string
		tokenPath string
		want      string
	}{
		{"fintlink.cn", "W725G084H61CF8A5C8", "_W725G084H61CF8A5C8.fintlink.cn"},
		{"www.example.org", "token_label", "_token_label.www.example.org"},
		{"www.example.org", "_token_label", "_token_label.www.example.org"},
		{"www.example.org", "", "_acme-challenge.www.example.org"},
		{"www.example.org", "_token_label.www.example.org", "_token_label.www.example.org"},
	}
	for _, test := range tests {
		got, err := zotrusDNSRecordName(test.domain, test.tokenPath)
		if err != nil {
			t.Fatalf("%s/%s: %v", test.domain, test.tokenPath, err)
		}
		if got != test.want {
			t.Fatalf("%s/%s: got %q want %q", test.domain, test.tokenPath, got, test.want)
		}
	}
	if _, err := zotrusDNSRecordName("example.com", "/.well-known/pki-validation/x.txt"); err == nil {
		t.Fatal("HTTP tokenPath 应当被拒绝")
	}
}

func TestZotrusDNSRecordType(t *testing.T) {
	for _, value := range []string{"", "TXT", "txt"} {
		got, err := zotrusDNSRecordType(value)
		if err != nil || got != "TXT" {
			t.Fatalf("%q: got %q err %v", value, got, err)
		}
	}
	if got, err := zotrusDNSRecordType("CNAME"); err != nil || got != "CNAME" {
		t.Fatalf("CNAME: got %q err %v", got, err)
	}
	if _, err := zotrusDNSRecordType("A"); err == nil {
		t.Fatal("A 记录应当被拒绝")
	}
}

func TestZotrusAbsoluteURL(t *testing.T) {
	if got := zotrusAbsoluteURL("https://ca.example/acme/order/1", "finalize"); got != "https://ca.example/acme/order/finalize" {
		t.Fatalf("相对路径解析错误: %q", got)
	}
	if got := zotrusAbsoluteURL("https://ca.example/acme/order/1", "https://ca.example/x"); got != "https://ca.example/x" {
		t.Fatalf("绝对路径不应改动: %q", got)
	}
	if got := zotrusAbsoluteURL("https://ca.example/acme/order/1", ""); got != "" {
		t.Fatalf("空路径应返回空: %q", got)
	}
}

func TestParseZotrusAuthorization(t *testing.T) {
	raw := []byte(`{
		"status":"pending",
		"identifier":{"type":"dns","value":"fintlink.cn"},
		"challenges":[{
			"type":"dns-01",
			"url":"https://my.zotrus.com/cnacme/chall/1",
			"status":"pending",
			"token":"evaGxfADs6pSRb2LAv9IZf17Dt3juxGJ-PCt92wr-oA",
			"tokenType":"TXT",
			"tokenPath":"W725G084H61CF8A5C8"
		}]
	}`)
	payload, ok := parseZotrusAuthorization(raw)
	if !ok {
		t.Fatal("解析失败")
	}
	if payload.Identifier.Value != "fintlink.cn" {
		t.Fatalf("domain = %q", payload.Identifier.Value)
	}
	if len(payload.Challenges) != 1 {
		t.Fatalf("challenges = %d", len(payload.Challenges))
	}
	challenge := payload.Challenges[0]
	if challenge.TokenPath != "W725G084H61CF8A5C8" || challenge.TokenType != "TXT" {
		t.Fatalf("扩展字段未解析: %+v", challenge)
	}
	if _, ok := parseZotrusAuthorization([]byte(`{"status":"pending"}`)); ok {
		t.Fatal("没有 challenges 的响应不应被视为 authorization")
	}
}

func TestZotrusCSROptions(t *testing.T) {
	options := zotrusCSROptions([]string{"example.com", "www.example.com"})
	if options.Domain != "example.com" {
		t.Fatalf("CommonName = %q", options.Domain)
	}
	if strings.Join(options.SAN, ",") != "example.com,www.example.com" {
		t.Fatalf("SAN = %v", options.SAN)
	}

	long := strings.Repeat("a", 70) + ".example.com"
	options = zotrusCSROptions([]string{long})
	if options.Domain != "" {
		t.Fatalf("超长域名不应作为 CommonName: %q", options.Domain)
	}
	if len(options.SAN) != 1 || options.SAN[0] != long {
		t.Fatalf("SAN = %v", options.SAN)
	}
}
