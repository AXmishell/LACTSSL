package pluginrepo

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// SignatureEnvelope 是索引的 JSON 签名封套，用于携带 key_id、索引摘要
// 和多个签名，便于密钥轮换与审计。旧仓库仍然可以使用单个 base64 签名文件。
type SignatureEnvelope struct {
	Schema      int                 `json:"schema"`
	IndexSHA256 string              `json:"index_sha256"`
	Revision    uint64              `json:"revision,omitempty"`
	SignedAt    string              `json:"signed_at,omitempty"`
	Signatures  []EnvelopeSignature `json:"signatures"`
}

// EnvelopeSignature 是封套中的单个签名。
type EnvelopeSignature struct {
	Alg       string `json:"alg"`
	KeyID     string `json:"key_id,omitempty"`
	Signature string `json:"signature"`
}

// maxSignatureBytes 是签名封套的大小上限。
const maxSignatureBytes = 16 << 10

// VerifyIndexSignature 使用 base64 编码的 Ed25519 公钥校验索引原始字节。
// signature 可以是旧格式的 64 字节签名，也可以是 JSON 签名封套。
// 304 场景必须重新调用它，不能仅因为“配置了公钥”就认为缓存已验签。
func VerifyIndexSignature(publicKeys string, raw, signature []byte) bool {
	_, ok := VerifyIndexSignatureKeyID(publicKeys, raw, signature)
	return ok
}

// VerifyIndexSignatureKeyID 与 VerifyIndexSignature 相同，并返回实际命中的公钥指纹。
func VerifyIndexSignatureKeyID(publicKeys string, raw, signature []byte) (string, bool) {
	keys, err := ParsePublicKeys(publicKeys)
	if err != nil || len(keys) == 0 {
		return "", false
	}
	return verifySignatureBlob(keys, raw, signature)
}

func verifySignatureBlob(keys []ed25519.PublicKey, raw, blob []byte) (string, bool) {
	// 旧式签名是 64 字节二进制，可能恰好以 '{' 开头或被空白字节包围。
	// 必须先按长度判断，否则合法签名会被误当成 JSON 封套而拒绝。
	if len(blob) == ed25519.SignatureSize {
		for _, key := range keys {
			if ed25519.Verify(key, raw, blob) {
				return PublicKeyFingerprint(key), true
			}
		}
		return "", false
	}
	trimmed := strings.TrimSpace(string(blob))
	if strings.HasPrefix(trimmed, "{") {
		var envelope SignatureEnvelope
		if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
			return "", false
		}
		if envelope.Schema != 1 || len(envelope.Signatures) == 0 {
			return "", false
		}
		sum := sha256.Sum256(raw)
		if !strings.EqualFold(strings.TrimSpace(envelope.IndexSHA256), hex.EncodeToString(sum[:])) {
			return "", false
		}
		for _, item := range envelope.Signatures {
			if item.Alg != "" && !strings.EqualFold(item.Alg, "ed25519") {
				continue
			}
			sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(item.Signature))
			if err != nil || len(sig) != ed25519.SignatureSize {
				continue
			}
			for _, key := range keys {
				if !ed25519.Verify(key, raw, sig) {
					continue
				}
				fingerprint := PublicKeyFingerprint(key)
				// key_id 只用于审计；如果声明了但与实际验签钥不一致，不能把它当作有效签名。
				if item.KeyID != "" && !strings.EqualFold(item.KeyID, fingerprint) {
					continue
				}
				return fingerprint, true
			}
		}
		return "", false
	}
	return "", false
}
