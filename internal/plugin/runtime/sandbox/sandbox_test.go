package sandbox

import "testing"

func TestRunCommandRejectsInvalidArgs(t *testing.T) {
	dir := t.TempDir()
	cases := map[string][]string{
		"empty":            {},
		"missing rw value": {"--rw"},
		"missing ro value": {"--ro"},
		"unknown flag":     {"--unknown"},
		"bad cpu value":    {"--cpu", "abc", "--rw", dir, "--", "/bin/true"},
		"negative cpu":     {"--cpu", "-1", "--rw", dir, "--", "/bin/true"},
		"missing command":  {"--rw", dir},
		"empty command":    {"--rw", dir, "--"},
	}
	for name, args := range cases {
		if err := RunCommand(args); err == nil {
			t.Fatalf("%s: expected error for %q", name, args)
		}
	}
}

func TestSupportsDoesNotPanic(t *testing.T) {
	_ = Supports()
}
