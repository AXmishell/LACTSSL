// Command signer 是插件索引的独立签名/验签工具。
//
// 该程序只依赖标准库，刻意不导入 catalog、i18n、notify、dns、deploy
// 等仓库包，避免 go run 时执行插件 init()。发布签名应在独立、受保护的
// 环境中调用本命令，私钥通过受控文件或外部 KMS/HSM 提供。
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

const signatureEnvelopeSchema = 1

type signatureEnvelope struct {
	Schema      int                 `json:"schema"`
	IndexSHA256 string              `json:"index_sha256"`
	Revision    uint64              `json:"revision,omitempty"`
	SignedAt    string              `json:"signed_at,omitempty"`
	Signatures  []envelopeSignature `json:"signatures"`
}

type envelopeSignature struct {
	Alg       string `json:"alg"`
	KeyID     string `json:"key_id,omitempty"`
	Signature string `json:"signature"`
}

func main() {
	if len(os.Args) < 2 {
		fail("用法: signer sign|verify|keygen [flags]")
	}
	switch os.Args[1] {
	case "sign":
		cmdSign(os.Args[2:])
	case "verify":
		cmdVerify(os.Args[2:])
	case "keygen":
		cmdKeygen(os.Args[2:])
	default:
		fail("未知子命令: " + os.Args[1])
	}
}

func cmdKeygen(args []string) {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("out", "", "新私钥输出路径（PEM，0600）")
	pubOut := fs.String("public-out", "", "公钥输出路径（base64）")
	fs.Parse(args)
	if *out == "" {
		fail("keygen 需要 -out")
	}
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		fail("生成密钥失败: " + err.Error())
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		fail("编码私钥失败: " + err.Error())
	}
	f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		fail("创建私钥文件失败: " + err.Error())
	}
	if err := pem.Encode(f, &pem.Block{Type: "PRIVATE KEY", Bytes: der}); err != nil {
		_ = f.Close()
		fail("写入私钥失败: " + err.Error())
	}
	if err := f.Close(); err != nil {
		fail("关闭私钥文件失败: " + err.Error())
	}
	pubText := base64.StdEncoding.EncodeToString(pub)
	if *pubOut != "" {
		if err := os.WriteFile(*pubOut, []byte(pubText+"\n"), 0o644); err != nil {
			fail("写入公钥失败: " + err.Error())
		}
	}
	fmt.Printf("private: %s (0600)\npublic: %s\nfingerprint: %s\n", *out, pubText, publicKeyFingerprint(pub))
}

func cmdSign(args []string) {
	fs := flag.NewFlagSet("sign", flag.ExitOnError)
	keyPath := fs.String("key", "", "base64 Ed25519 私钥文件（32 字节 seed 或 64 字节私钥）")
	in := fs.String("in", "index.json", "待签名文件")
	out := fs.String("out", "index.json.sig", "签名输出文件")
	envelope := fs.Bool("envelope", false, "输出 JSON 签名封套（含 key_id）")
	merge := fs.Bool("merge", false, "合并到已有签名封套（用于双签/密钥轮换）")
	revision := fs.Uint64("revision", 0, "封套中的索引 revision；留空读取 index.json")
	if err := fs.Parse(args); err != nil {
		fail(err.Error())
	}
	if *keyPath == "" {
		fail("sign 需要 -key")
	}
	if *merge && !*envelope {
		fail("-merge 必须与 -envelope 一起使用")
	}
	key, err := readKey(*keyPath)
	if err != nil {
		fail(err.Error())
	}
	if info, statErr := os.Stat(*keyPath); statErr == nil && info.Mode().Perm()&0o077 != 0 {
		fmt.Fprintln(os.Stderr, "signer: warning: 私钥文件权限过宽，建议 chmod 600")
	}
	message, err := os.ReadFile(*in)
	if err != nil {
		fail("读取待签名文件失败: " + err.Error())
	}
	sig := sign(key, message)
	if !*envelope {
		if err := os.WriteFile(*out, []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0o644); err != nil {
			fail("写入签名失败: " + err.Error())
		}
		fmt.Printf("wrote %s\n", *out)
		return
	}

	sum := sha256.Sum256(message)
	indexRevision := indexRevisionOf(message)
	if *revision > 0 {
		if indexRevision > 0 && indexRevision != *revision {
			fail(fmt.Sprintf("revision 不一致: index=%d flag=%d", indexRevision, *revision))
		}
		indexRevision = *revision
	}
	pub := key.Public().(ed25519.PublicKey)
	entry := envelopeSignature{
		Alg: "ed25519", KeyID: publicKeyFingerprint(pub),
		Signature: base64.StdEncoding.EncodeToString(sig),
	}
	envelopeValue := signatureEnvelope{
		Schema: signatureEnvelopeSchema, IndexSHA256: hex.EncodeToString(sum[:]),
		Revision: indexRevision, SignedAt: time.Now().UTC().Format(time.RFC3339),
		Signatures: []envelopeSignature{entry},
	}
	if *merge {
		existing, err := readEnvelope(*out)
		if err == nil {
			if existing.IndexSHA256 != envelopeValue.IndexSHA256 {
				fail("已有签名封套对应不同的索引，拒绝合并")
			}
			if existing.Revision != 0 && envelopeValue.Revision != 0 && existing.Revision != envelopeValue.Revision {
				fail(fmt.Sprintf("已有签名封套的 revision 不一致: existing=%d new=%d", existing.Revision, envelopeValue.Revision))
			}
			envelopeValue.SignedAt = existing.SignedAt
			envelopeValue.Signatures = mergeSignatures(existing.Signatures, entry)
		} else if !errors.Is(err, os.ErrNotExist) {
			fail("读取已有签名封套失败: " + err.Error())
		}
	}
	raw, err := json.MarshalIndent(envelopeValue, "", "  ")
	if err != nil {
		fail("编码签名封套失败: " + err.Error())
	}
	if err := os.WriteFile(*out, append(raw, '\n'), 0o644); err != nil {
		fail("写入签名封套失败: " + err.Error())
	}
	fmt.Printf("wrote %s\n", *out)
}

func indexRevisionOf(message []byte) uint64 {
	var index struct {
		Revision uint64 `json:"revision"`
	}
	if json.Unmarshal(message, &index) != nil {
		return 0
	}
	return index.Revision
}

func readEnvelope(path string) (signatureEnvelope, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return signatureEnvelope{}, err
	}
	var value signatureEnvelope
	if err := json.Unmarshal(raw, &value); err != nil {
		return signatureEnvelope{}, err
	}
	if value.Schema != signatureEnvelopeSchema {
		return signatureEnvelope{}, fmt.Errorf("签名封套 schema 不支持: %d", value.Schema)
	}
	return value, nil
}

func mergeSignatures(existing []envelopeSignature, add envelopeSignature) []envelopeSignature {
	out := make([]envelopeSignature, 0, len(existing)+1)
	for _, item := range existing {
		if item.KeyID == add.KeyID {
			continue
		}
		out = append(out, item)
	}
	out = append(out, add)
	return out
}

func publicKeyFingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

func cmdVerify(args []string) {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	pubValue := fs.String("pub", "", "base64 Ed25519 公钥")
	pubFile := fs.String("pub-file", "", "base64 Ed25519 公钥文件")
	in := fs.String("in", "index.json", "被签名文件")
	sigPath := fs.String("sig", "index.json.sig", "签名文件")
	if err := fs.Parse(args); err != nil {
		fail(err.Error())
	}

	pubText := strings.TrimSpace(*pubValue)
	if pubText == "" && *pubFile != "" {
		raw, err := os.ReadFile(*pubFile)
		if err != nil {
			fail("读取公钥失败: " + err.Error())
		}
		pubText = strings.TrimSpace(string(raw))
	}
	if pubText == "" {
		fail("verify 需要 -pub 或 -pub-file")
	}
	pubs, err := decodePublicKeys(pubText)
	if err != nil {
		fail(err.Error())
	}
	message, err := os.ReadFile(*in)
	if err != nil {
		fail("读取被签名文件失败: " + err.Error())
	}
	rawSig, err := os.ReadFile(*sigPath)
	if err != nil {
		fail("读取签名失败: " + err.Error())
	}
	trimmed := strings.TrimSpace(string(rawSig))
	if strings.HasPrefix(trimmed, "{") {
		keyID, err := verifyEnvelope(pubs, message, []byte(trimmed))
		if err != nil {
			fail(err.Error())
		}
		fmt.Printf("signature valid (key_id=%s)\n", keyID)
		return
	}
	sig, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		fail("解析签名失败: " + err.Error())
	}
	if !verifyAny(pubs, message, sig) {
		fail("签名无效")
	}
	fmt.Println("signature valid")
}

func verifyEnvelope(pubs []ed25519.PublicKey, message, raw []byte) (string, error) {
	var envelope signatureEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", fmt.Errorf("解析签名封套失败: %v", err)
	}
	if envelope.Schema != signatureEnvelopeSchema || len(envelope.Signatures) == 0 {
		return "", errors.New("签名封套格式不正确")
	}
	sum := sha256.Sum256(message)
	if !strings.EqualFold(envelope.IndexSHA256, hex.EncodeToString(sum[:])) {
		return "", errors.New("签名封套的索引摘要不匹配")
	}
	indexRevision := indexRevisionOf(message)
	if envelope.Revision != 0 && indexRevision != 0 && envelope.Revision != indexRevision {
		return "", fmt.Errorf("签名封套的 revision 与索引不一致: envelope=%d index=%d", envelope.Revision, indexRevision)
	}
	for _, item := range envelope.Signatures {
		if item.Alg != "" && !strings.EqualFold(item.Alg, "ed25519") {
			continue
		}
		sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(item.Signature))
		if err != nil {
			continue
		}
		if item.KeyID != "" {
			key, ok := keyForID(pubs, item.KeyID)
			if !ok || !verify(key, message, sig) {
				continue
			}
		} else if !verifyAny(pubs, message, sig) {
			continue
		}
		return item.KeyID, nil
	}
	return "", errors.New("签名无效")
}

func readKey(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取私钥失败: %w", err)
	}
	text := strings.TrimSpace(string(raw))
	if block, _ := pem.Decode([]byte(text)); block != nil {
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("解析 PEM 私钥失败: %w", err)
		}
		edKey, ok := key.(ed25519.PrivateKey)
		if !ok {
			return nil, errors.New("PEM 私钥不是 Ed25519")
		}
		return edKey, nil
	}
	key, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		return nil, fmt.Errorf("解析私钥失败: %w", err)
	}
	switch len(key) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(key), nil
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(key), nil
	default:
		return nil, fmt.Errorf("私钥长度必须是 32 或 64 字节，实际 %d", len(key))
	}
}

func decodePublicKey(value string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("解析公钥失败: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("公钥长度必须是 32 字节")
	}
	return ed25519.PublicKey(raw), nil
}

func decodePublicKeys(value string) ([]ed25519.PublicKey, error) {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	if len(fields) == 0 {
		return nil, errors.New("公钥不能为空")
	}
	keys := make([]ed25519.PublicKey, 0, len(fields))
	for _, field := range fields {
		key, err := decodePublicKey(field)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func keyForID(keys []ed25519.PublicKey, keyID string) (ed25519.PublicKey, bool) {
	for _, key := range keys {
		if strings.EqualFold(publicKeyFingerprint(key), strings.TrimSpace(keyID)) {
			return key, true
		}
	}
	return nil, false
}

func verifyAny(keys []ed25519.PublicKey, message, sig []byte) bool {
	for _, key := range keys {
		if verify(key, message, sig) {
			return true
		}
	}
	return false
}

func sign(key ed25519.PrivateKey, message []byte) []byte {
	return ed25519.Sign(key, message)
}

func verify(pub ed25519.PublicKey, message, sig []byte) bool {
	if len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pub, message, sig)
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, "signer: "+message)
	os.Exit(1)
}
