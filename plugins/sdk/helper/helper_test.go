package helper

import (
	"strings"
	"testing"

	"github.com/AXmishell/LACTSSL-Plugins/protocol"
)

func TestReadJSONLimits(t *testing.T) {
	var value struct {
		OK bool `json:"ok"`
	}
	if err := readJSON(strings.NewReader(`{"ok":true}`), &value); err != nil || !value.OK {
		t.Fatalf("valid JSON: %v %+v", err, value)
	}
	if err := readJSON(strings.NewReader(strings.Repeat(" ", maxJSONSize+1)), &value); err == nil {
		t.Fatal("oversized input should fail")
	}
	if err := readJSON(strings.NewReader(""), &value); err == nil {
		t.Fatal("empty input should fail")
	}
}

func TestHasFeatureIn(t *testing.T) {
	t.Setenv(protocol.DeployFeaturesEnv, "a,b")
	t.Setenv(protocol.DNSFeaturesEnv, "")
	if !HasFeatureIn(protocol.DeployFeaturesEnv, "b") {
		t.Fatal("feature b should be present")
	}
	if HasFeatureIn(protocol.DNSFeaturesEnv, "b") {
		t.Fatal("DNS features must not see deploy features")
	}
}
