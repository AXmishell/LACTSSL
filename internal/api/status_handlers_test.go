package api

import "testing"

func TestParseProcessStats(t *testing.T) {
	const status = `Name:	lactssl
State:	S (sleeping)
VmPeak:	  1357600 kB
VmSize:	  1357600 kB
VmRSS:	    14044 kB
RssAnon:	     8216 kB
VmData:	   135496 kB
VmSwap:	     6624 kB
Threads:	8
`
	stats := parseProcessStats(status)
	if stats.rss != 14044*1024 {
		t.Fatalf("VmRSS = %d, want %d", stats.rss, 14044*1024)
	}
	if stats.anon != 8216*1024 {
		t.Fatalf("RssAnon = %d, want %d", stats.anon, 8216*1024)
	}
	if stats.vsz != 1357600*1024 {
		t.Fatalf("VmSize = %d, want %d", stats.vsz, 1357600*1024)
	}
	if stats.swap != 6624*1024 {
		t.Fatalf("VmSwap = %d, want %d", stats.swap, 6624*1024)
	}
	if stats.threads != 8 {
		t.Fatalf("Threads = %d, want %d", stats.threads, 8)
	}
}

func TestParseProcessStatsMissingFields(t *testing.T) {
	stats := parseProcessStats("Name:\tlactssl\n")
	if stats.rss != 0 || stats.anon != 0 || stats.vsz != 0 || stats.swap != 0 || stats.threads != 0 {
		t.Fatalf("缺少字段时应返回零值，实际为 %+v", stats)
	}
}
