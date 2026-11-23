package indexschema

import (
	"strings"
	"testing"
)

const testSHA = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

func validDocument() string {
	return `{
		"schema": 1,
		"channel": "stable",
		"protocol": "v1",
		"revision": 3,
		"generated_at": "2026-01-01T00:00:00Z",
		"plugins": [{
			"kind": "dns",
			"id": "demo",
			"version": "1.0.0",
			"protocol": "v1",
			"providers": ["demo"],
			"bundle": true,
			"assets": {
				"linux/amd64": {
					"url": "https://example.invalid/demo.tgz",
					"sha256": "` + testSHA + `",
					"size": 1
				}
			}
		}],
		"icons": []
	}`
}

func TestValidateAcceptsGeneratedIndex(t *testing.T) {
	if err := Validate([]byte(validDocument())); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsUnknownFields(t *testing.T) {
	document := strings.Replace(validDocument(), `"schema": 1,`, `"schema": 1, "future": true,`, 1)
	if err := Validate([]byte(document)); err == nil || !strings.Contains(err.Error(), "future") {
		t.Fatalf("unknown root field should fail: %v", err)
	}
	document = strings.Replace(validDocument(), `"protocol": "v1",`, `"protocol": "v1", "future_field": true,`, 1)
	if err := Validate([]byte(document)); err == nil || !strings.Contains(err.Error(), "future_field") {
		t.Fatalf("unknown plugin field should fail: %v", err)
	}
}

func TestValidateRejectsMissingSHA256(t *testing.T) {
	document := strings.Replace(validDocument(), `"sha256": "`+testSHA+`",`, `"sha256": "",`, 1)
	if err := Validate([]byte(document)); err == nil {
		t.Fatal("empty sha256 should fail")
	}
}

func TestValidateRejectsBadRevisionAndTime(t *testing.T) {
	document := strings.Replace(validDocument(), `"revision": 3`, `"revision": 0`, 1)
	if err := Validate([]byte(document)); err == nil {
		t.Fatal("revision 0 should fail")
	}
	document = strings.Replace(validDocument(), `"generated_at": "2026-01-01T00:00:00Z"`, `"generated_at": "yesterday"`, 1)
	if err := Validate([]byte(document)); err == nil {
		t.Fatal("invalid date-time should fail")
	}
	document = strings.Replace(validDocument(), `"generated_at": "2026-01-01T00:00:00Z"`, `"generated_at": "2026-01-01T00:00:00Z", "expires_at": "later"`, 1)
	if err := Validate([]byte(document)); err == nil {
		t.Fatal("invalid expires_at should fail")
	}
}

func TestValidateRejectsDuplicateProviders(t *testing.T) {
	document := strings.Replace(validDocument(), `"providers": ["demo"]`, `"providers": ["demo", "demo"]`, 1)
	if err := Validate([]byte(document)); err == nil {
		t.Fatal("duplicate providers should fail")
	}
}

func TestEmbeddedSchemaParses(t *testing.T) {
	if len(SchemaJSON) == 0 {
		t.Fatal("embedded schema is empty")
	}
	if err := ValidateWithSchema(SchemaJSON, []byte(validDocument())); err != nil {
		t.Fatal(err)
	}
}
