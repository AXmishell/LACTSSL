package validate

import "testing"

func TestRegion(t *testing.T) {
	for _, value := range []string{"cn-hangzhou", "us-east-1", "ap-southeast-1"} {
		if err := Region(value); err != nil {
			t.Fatalf("Region(%q): %v", value, err)
		}
	}
	for _, value := range []string{"", "a@evil.com/", "cn hangzhou", "../x", "CN-HANGZHOU"} {
		if err := Region(value); err == nil {
			t.Fatalf("Region(%q) should fail", value)
		}
	}
	got, err := RegionOr("", "cn-hangzhou")
	if err != nil || got != "cn-hangzhou" {
		t.Fatalf("RegionOr = %q, %v", got, err)
	}
}
