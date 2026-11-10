package api

import "testing"

func TestParseProcessMemory(t *testing.T) {
	const status = `Name:	lactssl
State:	S (sleeping)
VmPeak:	  1357600 kB
VmSize:	  1357600 kB
VmRSS:	    14044 kB
VmData:	   135496 kB
VmSwap:	     6624 kB
Threads:	8
`
	rss, swap := parseProcessMemory(status)
	if rss != 14044*1024 {
		t.Fatalf("VmRSS = %d, want %d", rss, 14044*1024)
	}
	if swap != 6624*1024 {
		t.Fatalf("VmSwap = %d, want %d", swap, 6624*1024)
	}
}

func TestParseProcessMemoryMissingFields(t *testing.T) {
	if rss, swap := parseProcessMemory("Name:\tlactssl\n"); rss != 0 || swap != 0 {
		t.Fatalf("缺少字段时应返回 0, 0，实际为 %d, %d", rss, swap)
	}
}
