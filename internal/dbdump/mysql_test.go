package dbdump

import (
	"testing"
	"time"
)

func TestQuoteMySQLString(t *testing.T) {
	cases := map[string]string{
		"plain":    "'plain'",
		"a'b":      `'a\'b'`,
		"a\\b":     `'a\\b'`,
		"line\n":   `'line\n'`,
		"zero\x00": `'zero\0'`,
	}
	for input, want := range cases {
		if got := quoteMySQLString(input); got != want {
			t.Fatalf("quoteMySQLString(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestMySQLLiteral(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cases := []struct {
		value  any
		binary bool
		want   string
	}{
		{nil, false, "NULL"},
		{"text", false, "'text'"},
		{[]byte("text"), false, "'text'"},
		{[]byte{0xff, 0x00}, false, "X'ff00'"},
		{[]byte{0xff}, true, "X'ff'"},
		{int64(42), false, "42"},
		{true, false, "1"},
		{now, false, "'2026-01-02 03:04:05'"},
	}
	for _, tc := range cases {
		got, err := mysqlLiteral(tc.value, tc.binary)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Fatalf("mysqlLiteral(%T, %v) = %q, want %q", tc.value, tc.binary, got, tc.want)
		}
	}
}

func TestNormalizeMySQLCreate(t *testing.T) {
	input := "CREATE TABLE `t` (\n  `id` bigint\n);"
	want := "CREATE TABLE `t` (   `id` bigint );"
	if got := normalizeMySQLCreate(input); got != want {
		t.Fatalf("normalizeMySQLCreate = %q, want %q", got, want)
	}
}
