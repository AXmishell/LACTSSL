package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSignAndVerify(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	message := []byte(`{"schema":1}`)
	sig := sign(priv, message)
	if !verify(pub, message, sig) {
		t.Fatal("signature should verify")
	}
	if verify(pub, []byte("other"), sig) {
		t.Fatal("modified message must not verify")
	}
}

func TestReadKeySeedAndPrivate(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	seedPath := filepath.Join(dir, "seed.b64")
	if err := os.WriteFile(seedPath, []byte(base64.StdEncoding.EncodeToString(priv.Seed())), 0o600); err != nil {
		t.Fatal(err)
	}
	fromSeed, err := readKey(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !fromSeed.Equal(priv) {
		t.Fatal("seed-derived key mismatch")
	}
	privPath := filepath.Join(dir, "private.b64")
	if err := os.WriteFile(privPath, []byte(base64.StdEncoding.EncodeToString(priv)), 0o600); err != nil {
		t.Fatal(err)
	}
	fromPrivate, err := readKey(privPath)
	if err != nil {
		t.Fatal(err)
	}
	if !fromPrivate.Equal(priv) {
		t.Fatal("private key mismatch")
	}
}

func TestReadPEMKey(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(priv) {
		t.Fatal("PEM key mismatch")
	}
}

func TestVerifyRejectsWrongLength(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if verify(pub, []byte("x"), []byte{1, 2, 3}) {
		t.Fatal("short signature must be rejected")
	}
}

func TestVerifyAnyKey(t *testing.T) {
	pubA, privA, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubB, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	value := base64.StdEncoding.EncodeToString(pubA) + "," + base64.StdEncoding.EncodeToString(pubB)
	keys, err := decodePublicKeys(value)
	if err != nil || len(keys) != 2 {
		t.Fatalf("decodePublicKeys = %d, %v", len(keys), err)
	}
	message := []byte("index")
	if !verifyAny(keys, message, sign(privA, message)) {
		t.Fatal("signature from either key must verify")
	}
}

func TestSignEnvelopeAndVerify(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key.b64")
	if err := os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(priv.Seed())), 0o600); err != nil {
		t.Fatal(err)
	}
	index := []byte(`{"schema":1,"channel":"stable","protocol":"v1","revision":4,"plugins":[]}`)
	indexPath := filepath.Join(dir, "index.json")
	if err := os.WriteFile(indexPath, index, 0o600); err != nil {
		t.Fatal(err)
	}
	sigPath := filepath.Join(dir, "index.json.sig.json")
	cmdSign([]string{"-key", keyPath, "-in", indexPath, "-out", sigPath, "-envelope"})

	raw, err := os.ReadFile(sigPath)
	if err != nil {
		t.Fatal(err)
	}
	var envelope signatureEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(index)
	if envelope.Schema != 1 || envelope.Revision != 4 || envelope.IndexSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("envelope = %+v", envelope)
	}
	if len(envelope.Signatures) != 1 || envelope.Signatures[0].KeyID != publicKeyFingerprint(pub) {
		t.Fatalf("signatures = %+v", envelope.Signatures)
	}
	cmdVerify([]string{"-pub", base64.StdEncoding.EncodeToString(pub), "-in", indexPath, "-sig", sigPath})
}

func TestSignEnvelopeMerge(t *testing.T) {
	pubA, privA, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubB, privB, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "index.json")
	index := []byte(`{"schema":1,"revision":5,"plugins":[]}`)
	if err := os.WriteFile(indexPath, index, 0o600); err != nil {
		t.Fatal(err)
	}
	keyA := filepath.Join(dir, "a.b64")
	keyB := filepath.Join(dir, "b.b64")
	if err := os.WriteFile(keyA, []byte(base64.StdEncoding.EncodeToString(privA.Seed())), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyB, []byte(base64.StdEncoding.EncodeToString(privB.Seed())), 0o600); err != nil {
		t.Fatal(err)
	}
	sigPath := filepath.Join(dir, "index.json.sig.json")
	cmdSign([]string{"-key", keyA, "-in", indexPath, "-out", sigPath, "-envelope"})
	cmdSign([]string{"-key", keyB, "-in", indexPath, "-out", sigPath, "-envelope", "-merge"})

	raw, err := os.ReadFile(sigPath)
	if err != nil {
		t.Fatal(err)
	}
	var envelope signatureEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Signatures) != 2 {
		t.Fatalf("merged signatures = %+v", envelope.Signatures)
	}
	keys := base64.StdEncoding.EncodeToString(pubA) + "," + base64.StdEncoding.EncodeToString(pubB)
	cmdVerify([]string{"-pub", keys, "-in", indexPath, "-sig", sigPath})
}

func TestVerifyEnvelopeRejectsRevisionAndKeyIDMismatch(t *testing.T) {
	pubA, privA, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubB, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	message := []byte(`{"schema":1,"revision":5,"plugins":[]}`)
	sum := sha256.Sum256(message)
	sig := sign(privA, message)
	envelope := signatureEnvelope{
		Schema: signatureEnvelopeSchema, IndexSHA256: hex.EncodeToString(sum[:]), Revision: 5,
		Signatures: []envelopeSignature{{
			Alg: "ed25519", KeyID: publicKeyFingerprint(pubA),
			Signature: base64.StdEncoding.EncodeToString(sig),
		}},
	}
	raw, _ := json.Marshal(envelope)
	if _, err := verifyEnvelope([]ed25519.PublicKey{pubA}, message, raw); err != nil {
		t.Fatalf("valid envelope: %v", err)
	}

	tampered := envelope
	tampered.Revision = 6
	raw, _ = json.Marshal(tampered)
	if _, err := verifyEnvelope([]ed25519.PublicKey{pubA}, message, raw); err == nil {
		t.Fatal("revision mismatch must fail")
	}

	tampered = envelope
	tampered.Signatures[0].KeyID = publicKeyFingerprint(pubB)
	raw, _ = json.Marshal(tampered)
	if _, err := verifyEnvelope([]ed25519.PublicKey{pubA, pubB}, message, raw); err == nil {
		t.Fatal("key_id must match the key that produced the signature")
	}
}

func TestKeygen(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "new.pem")
	pubPath := filepath.Join(dir, "new.pub")
	cmdKeygen([]string{"-out", keyPath, "-public-out", pubPath})
	key, err := readKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if key.Public().(ed25519.PublicKey) == nil {
		t.Fatal("missing public key")
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("private key permissions = %o", info.Mode().Perm())
	}
	raw, err := os.ReadFile(pubPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodePublicKey(strings.TrimSpace(string(raw))); err != nil {
		t.Fatal(err)
	}
}
