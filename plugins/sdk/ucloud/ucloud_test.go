package ucloud

import "testing"

func TestSignMatchesUCloudRule(t *testing.T) {
	params := map[string]string{"Action": "GetProjectList", "PublicKey": "test"}
	got := Sign(params, "secret")
	want := "4acac64fa7cf7160a0cb698d1507c4aa43cdc5a8"
	if got != want {
		t.Fatalf("signature = %q, want %q", got, want)
	}
}

func TestEncodeAddsPublicKeyAndSignature(t *testing.T) {
	body := Encode(map[string]string{"Action": "GetProjectList"}, "public", "private")
	if body == "" {
		t.Fatal("empty body")
	}
	for _, part := range []string{"Action=GetProjectList", "PublicKey=public", "Signature="} {
		if !contains(body, part) {
			t.Fatalf("body %q does not contain %q", body, part)
		}
	}
}

func contains(value, part string) bool {
	return len(part) == 0 || (len(value) >= len(part) && index(value, part) >= 0)
}

func index(value, part string) int {
	for i := 0; i+len(part) <= len(value); i++ {
		if value[i:i+len(part)] == part {
			return i
		}
	}
	return -1
}
