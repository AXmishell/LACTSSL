package state

import (
	"errors"
	"testing"
)

type stringProvider struct {
	recordIDs map[string]string
}

func (p *stringProvider) Present(domain, token, keyAuth string) error {
	p.recordIDs[token] = "record-" + token
	return nil
}

func (p *stringProvider) CleanUp(domain, token, keyAuth string) error {
	if _, ok := p.recordIDs[token]; !ok {
		return errors.New("unknown record ID")
	}
	delete(p.recordIDs, token)
	return nil
}

func TestPresentThenCleanupInNewProvider(t *testing.T) {
	t.Setenv(stateDirEnv, t.TempDir())
	present := &stringProvider{recordIDs: map[string]string{}}
	if err := Wrap("demo", present).Present("example.com", "token-1", "key-auth"); err != nil {
		t.Fatal(err)
	}

	cleanup := &stringProvider{recordIDs: map[string]string{}}
	if err := Wrap("demo", cleanup).CleanUp("example.com", "token-1", "key-auth"); err != nil {
		t.Fatalf("cleanup in a new process should restore record ID: %v", err)
	}
	if _, ok := cleanup.recordIDs["token-1"]; ok {
		t.Fatal("record ID should be removed after cleanup")
	}
}

type intProvider struct {
	recordIDs map[string]int
}

func (p *intProvider) Present(domain, token, keyAuth string) error {
	p.recordIDs[token] = 42
	return nil
}

func (p *intProvider) CleanUp(domain, token, keyAuth string) error {
	if _, ok := p.recordIDs[token]; !ok {
		return errors.New("unknown record ID")
	}
	return nil
}

func TestIntRecordID(t *testing.T) {
	t.Setenv(stateDirEnv, t.TempDir())
	if err := Wrap("demo", &intProvider{recordIDs: map[string]int{}}).Present("example.com", "token-int", "key-auth"); err != nil {
		t.Fatal(err)
	}
	cleanup := &intProvider{recordIDs: map[string]int{}}
	if err := Wrap("demo", cleanup).CleanUp("example.com", "token-int", "key-auth"); err != nil {
		t.Fatal(err)
	}
	if cleanup.recordIDs["token-int"] != 42 {
		t.Fatalf("restored ID = %d", cleanup.recordIDs["token-int"])
	}
}

type pointerProvider struct {
	recordIDs map[string]*string
}

func (p *pointerProvider) Present(domain, token, keyAuth string) error {
	value := "record-" + token
	p.recordIDs[token] = &value
	return nil
}

func (p *pointerProvider) CleanUp(domain, token, keyAuth string) error {
	value, ok := p.recordIDs[token]
	if !ok || value == nil || *value == "" {
		return errors.New("unknown record ID")
	}
	return nil
}

func TestPointerRecordID(t *testing.T) {
	t.Setenv(stateDirEnv, t.TempDir())
	if err := Wrap("demo", &pointerProvider{recordIDs: map[string]*string{}}).Present("example.com", "token-ptr", "key-auth"); err != nil {
		t.Fatal(err)
	}
	cleanup := &pointerProvider{recordIDs: map[string]*string{}}
	if err := Wrap("demo", cleanup).CleanUp("example.com", "token-ptr", "key-auth"); err != nil {
		t.Fatal(err)
	}
	if cleanup.recordIDs["token-ptr"] == nil || *cleanup.recordIDs["token-ptr"] != "record-token-ptr" {
		t.Fatalf("restored pointer = %+v", cleanup.recordIDs["token-ptr"])
	}
}

type reqKey struct {
	domainID int
	recordID int
}

type structProvider struct {
	recordIDs map[string]reqKey
}

func (p *structProvider) Present(domain, token, keyAuth string) error {
	p.recordIDs[token] = reqKey{domainID: 7, recordID: 9}
	return nil
}

func (p *structProvider) CleanUp(domain, token, keyAuth string) error {
	if _, ok := p.recordIDs[token]; !ok {
		return errors.New("unknown record ID")
	}
	return nil
}

func TestStructRecordID(t *testing.T) {
	t.Setenv(stateDirEnv, t.TempDir())
	if err := Wrap("demo", &structProvider{recordIDs: map[string]reqKey{}}).Present("example.com", "token-struct", "key-auth"); err != nil {
		t.Fatal(err)
	}
	cleanup := &structProvider{recordIDs: map[string]reqKey{}}
	if err := Wrap("demo", cleanup).CleanUp("example.com", "token-struct", "key-auth"); err != nil {
		t.Fatal(err)
	}
	if got := cleanup.recordIDs["token-struct"]; got.domainID != 7 || got.recordID != 9 {
		t.Fatalf("restored key = %+v", got)
	}
}

type unwrapProvider struct {
	inner *stringProvider
}

func (p unwrapProvider) Present(domain, token, keyAuth string) error {
	return p.inner.Present(domain, token, keyAuth)
}

func (p unwrapProvider) CleanUp(domain, token, keyAuth string) error {
	return p.inner.CleanUp(domain, token, keyAuth)
}

func (p unwrapProvider) Unwrap() any { return p.inner }

func TestUnwrapProviderState(t *testing.T) {
	t.Setenv(stateDirEnv, t.TempDir())
	if err := Wrap("v5", unwrapProvider{inner: &stringProvider{recordIDs: map[string]string{}}}).Present("example.com", "token-v5", "key-auth"); err != nil {
		t.Fatal(err)
	}
	inner := &stringProvider{recordIDs: map[string]string{}}
	if err := Wrap("v5", unwrapProvider{inner: inner}).CleanUp("example.com", "token-v5", "key-auth"); err != nil {
		t.Fatalf("record ID was not restored through Unwrap: %v", err)
	}
}

type plainProvider struct{}

func (plainProvider) Present(string, string, string) error { return nil }
func (plainProvider) CleanUp(string, string, string) error { return nil }

func TestProviderWithoutRecordIDsIsPassthrough(t *testing.T) {
	t.Setenv(stateDirEnv, t.TempDir())
	wrapped := Wrap("native", plainProvider{})
	if err := wrapped.Present("example.com", "t", "k"); err != nil {
		t.Fatal(err)
	}
	if err := wrapped.CleanUp("example.com", "t", "k"); err != nil {
		t.Fatal(err)
	}
}
