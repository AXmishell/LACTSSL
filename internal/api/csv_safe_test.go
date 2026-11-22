package api

import "testing"

func TestCSVSafeNeutralizesFormulas(t *testing.T) {
	for _, value := range []string{"=cmd|' /C calc'!A0", "+1+1", "-2+3", "@SUM(A1)"} {
		if got := csvSafe(value); got != "'"+value {
			t.Fatalf("csvSafe(%q) = %q", value, got)
		}
	}
	for _, value := range []string{"normal", "  leading", ""} {
		if got := csvSafe(value); got != value {
			t.Fatalf("csvSafe(%q) 不应改变: %q", value, got)
		}
	}
}
