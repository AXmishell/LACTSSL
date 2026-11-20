package service

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

const (
	// maxChainPEMBytes 限制上游返回的证书链大小，避免异常响应占用内存。
	maxChainPEMBytes = 512 << 10
	// maxChainCertificates 限制单个完整链中的证书数量。
	maxChainCertificates = 16
)

// errInvalidChainPEM 表示上游返回的证书链包含非 CERTIFICATE 内容。
var errInvalidChainPEM = errors.New("certificate chain contains non-certificate data")

// NormalizeCertificateChain 解析 PEM 证书链，只接受 CERTIFICATE 块与块间空白，
// 拒绝任何尾随/夹杂的任意文本，并按 DER 去重后重新编码。
//
// 上游 ACME CA 的响应被视为不可信输入：lego 只解析 CERTIFICATE 块来定位 leaf，
// 不会拒绝块之后的额外文本。如果不在这里重新编码，恶意 CA 可以把 PHP/SSH/cron
// 内容塞进 FullchainPEM，再由本地部署原样写到任意路径。
func NormalizeCertificateChain(parts ...[]byte) ([]byte, []string, error) {
	total := 0
	for _, part := range parts {
		total += len(part)
		if total > maxChainPEMBytes {
			return nil, nil, fmt.Errorf("certificate chain exceeds %d bytes", maxChainPEMBytes)
		}
	}

	var (
		out     bytes.Buffer
		seen    = make(map[[sha256.Size]byte]bool)
		digests []string
		count   int
	)
	for _, part := range parts {
		rest := part
		for {
			// 只允许块之间出现空白；pem.Decode 会跳过前导垃圾，因此必须先
			// 显式检查当前位置就是 CERTIFICATE 头，否则尾随文本会被静默忽略。
			rest = bytes.TrimLeft(rest, " \t\r\n")
			if len(rest) == 0 {
				break
			}
			if !bytes.HasPrefix(rest, []byte("-----BEGIN CERTIFICATE-----")) {
				return nil, nil, errInvalidChainPEM
			}
			block, next := pem.Decode(rest)
			if block == nil || block.Type != "CERTIFICATE" {
				return nil, nil, errInvalidChainPEM
			}
			if _, err := x509.ParseCertificate(block.Bytes); err != nil {
				return nil, nil, fmt.Errorf("invalid certificate in chain: %w", err)
			}
			sum := sha256.Sum256(block.Bytes)
			if !seen[sum] {
				seen[sum] = true
				count++
				if count > maxChainCertificates {
					return nil, nil, fmt.Errorf("certificate chain exceeds %d certificates", maxChainCertificates)
				}
				encoded := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: block.Bytes})
				out.Write(encoded)
				digests = append(digests, fmt.Sprintf("%x", sum))
			}
			rest = next
		}
	}
	if out.Len() == 0 {
		return nil, nil, errors.New("certificate chain is empty")
	}
	return out.Bytes(), digests, nil
}

// BuildFullchainPEM 使用 leaf 作为首证书构建规范化完整链。
// extra 中的任意内容都必须只包含 CERTIFICATE 块；否则返回错误。
func BuildFullchainPEM(leaf *x509.Certificate, extra ...[]byte) (string, error) {
	if leaf == nil {
		return "", errors.New("leaf certificate is required")
	}
	leafBlock := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw})
	raw := make([]byte, 0, len(leafBlock)+maxChainPEMBytes)
	raw = append(raw, leafBlock...)
	for _, part := range extra {
		raw = append(raw, part...)
	}
	normalized, _, err := NormalizeCertificateChain(raw)
	if err != nil {
		return "", err
	}
	// NormalizeCertificateChain 保留输入顺序；leaf 已经在最前面，因此无需重排。
	return string(normalized), nil
}

// CertificateChainContains 判断规范化链中是否包含指定 DER。
func CertificateChainContains(chainPEM []byte, der []byte) bool {
	rest := chainPEM
	for {
		rest = bytes.TrimLeft(rest, " \t\r\n")
		if len(rest) == 0 {
			return false
		}
		block, next := pem.Decode(rest)
		if block == nil {
			return false
		}
		if bytes.Equal(block.Bytes, der) {
			return true
		}
		rest = next
	}
}

// ValidateCertificateChainPEM 用于部署前复核：只允许 PEM CERTIFICATE 块。
func ValidateCertificateChainPEM(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("certificate chain is empty")
	}
	_, _, err := NormalizeCertificateChain([]byte(value))
	return err
}
