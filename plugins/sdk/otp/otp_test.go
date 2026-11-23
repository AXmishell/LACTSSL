package otp

import (
	"testing"
	"time"
)

func TestRFC6238Vector(t *testing.T) {
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" // "12345678901234567890"
	code, err := CodeWith(secret, time.Unix(59, 0), 8, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if code != "94287082" {
		t.Fatalf("code = %q, want 94287082", code)
	}
}

func TestInvalidSecret(t *testing.T) {
	if _, err := Code("not-base32!", time.Now()); err == nil {
		t.Fatal("expected error")
	}
	if _, err := Code("", time.Now()); err == nil {
		t.Fatal("empty secret should fail")
	}
}

func TestSubSecondPeriodDoesNotPanic(t *testing.T) {
	if _, err := CodeWith("GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", time.Now(), 6, 500*time.Millisecond); err == nil {
		t.Fatal("sub-second period should fail")
	}
}
