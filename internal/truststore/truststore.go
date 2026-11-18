// Package truststore 维护系统级 TLS 信任链。
//
// 信任链由三部分组成：
//   - 操作系统根证书；
//   - 系统导入的 CA 公钥（trusted_cas）；
//   - 本程序私有 CA 的证书链。
//
// 主程序的证书监控、通知与部署插件共用同一份信任材料。子进程通过
// SSL_CERT_FILE 读取合并后的 PEM 文件，因此不会替换掉系统根证书。
package truststore

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"lactssl/internal/i18n"
	"lactssl/internal/model"
)

// BundleFileName 是合并后的信任链文件名，保存在数据目录中。
const BundleFileName = "trusted-ca-bundle.pem"

var systemBundlePaths = []string{
	"/etc/ssl/certs/ca-certificates.crt",
	"/etc/pki/tls/certs/ca-bundle.crt",
	"/etc/ssl/ca-bundle.pem",
	"/etc/pki/tls/cacert.pem",
	"/etc/ssl/cert.pem",
}

var (
	systemOnce sync.Once
	systemPEM  []byte
)

// SystemPEM 返回操作系统根证书的 PEM 内容。
// 优先使用未被本程序接管的 SSL_CERT_FILE，其次读取常见发行版路径。
func SystemPEM() []byte {
	systemOnce.Do(func() {
		candidates := make([]string, 0, len(systemBundlePaths)+3)
		for _, key := range []string{"SSL_CERT_FILE", "CURL_CA_BUNDLE", "REQUESTS_CA_BUNDLE"} {
			custom := strings.TrimSpace(os.Getenv(key))
			if custom == "" || filepath.Base(filepath.Clean(custom)) == BundleFileName {
				continue
			}
			candidates = append(candidates, custom)
		}
		candidates = append(candidates, systemBundlePaths...)
		for _, path := range candidates {
			data, err := os.ReadFile(path)
			if err != nil || len(bytes.TrimSpace(data)) == 0 {
				continue
			}
			systemPEM = data
			return
		}
	})
	return systemPEM
}

// HasSystemBundle 表示当前平台能找到可合并的系统根证书。
// 找不到时不应把 SSL_CERT_FILE 指向仅含内部 CA 的文件，否则会破坏公共 TLS。
func HasSystemBundle() bool {
	return len(bytes.TrimSpace(SystemPEM())) > 0
}

// ParseCAPEM 解析并校验待导入的 CA 证书。支持一个 PEM 中包含多张证书，
// 例如内部根 CA 及其下级 CA；所有证书都必须是未过期的 CA。
func ParseCAPEM(raw string) ([]*x509.Certificate, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, i18n.E("trust.error.cert_required")
	}
	rest := []byte(raw)
	now := time.Now()
	out := make([]*x509.Certificate, 0, 2)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, i18n.E("trust.error.cert_parse_failed", "error", err.Error())
		}
		if !cert.IsCA {
			return nil, i18n.E("trust.error.not_ca", "subject", cert.Subject.String())
		}
		if now.After(cert.NotAfter) {
			return nil, i18n.E("trust.error.cert_expired", "subject", cert.Subject.String())
		}
		out = append(out, cert)
	}
	if len(out) == 0 {
		return nil, i18n.E("trust.error.pem_invalid")
	}
	return out, nil
}

// CAFromCertificate 把 x509 证书转换为可持久化的记录。
// name 为空时使用证书主题中的通用名称。
func CAFromCertificate(cert *x509.Certificate, name string) *model.TrustedCA {
	name = strings.TrimSpace(name)
	if name == "" {
		name = strings.TrimSpace(cert.Subject.CommonName)
	}
	if name == "" {
		name = cert.Subject.String()
	}
	sum := sha256.Sum256(cert.Raw)
	serial := ""
	if cert.SerialNumber != nil {
		serial = strings.ToUpper(cert.SerialNumber.Text(16))
	}
	return &model.TrustedCA{
		Name:        name,
		Subject:     cert.Subject.String(),
		Issuer:      cert.Issuer.String(),
		Serial:      serial,
		Fingerprint: strings.ToUpper(hex.EncodeToString(sum[:])),
		CertPEM:     string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})),
		NotBefore:   cert.NotBefore,
		NotAfter:    cert.NotAfter,
	}
}

// CertPool 返回系统根证书与内部 CA 合并后的证书池。
func CertPool(extraPEM []string) *x509.CertPool {
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	for _, raw := range extraPEM {
		if strings.TrimSpace(raw) != "" {
			roots.AppendCertsFromPEM([]byte(raw))
		}
	}
	return roots
}

// BundlePEM 返回系统根证书与内部 CA 合并后的 PEM 内容。
func BundlePEM(extraPEM []string) []byte {
	var buf bytes.Buffer
	if system := bytes.TrimSpace(SystemPEM()); len(system) > 0 {
		buf.Write(system)
		buf.WriteByte('\n')
	}
	for _, raw := range extraPEM {
		value := bytes.TrimSpace([]byte(raw))
		if len(value) == 0 {
			continue
		}
		buf.Write(value)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// SyncBundle 把合并后的信任链原子写入数据目录，返回文件路径。
// dir 为空时只返回内存中的 PEM（path 为空），便于测试和嵌入式调用。
func SyncBundle(dir string, extraPEM []string) (string, error) {
	data := BundlePEM(extraPEM)
	if strings.TrimSpace(dir) == "" {
		return "", nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	target := filepath.Join(dir, BundleFileName)
	tmp, err := os.CreateTemp(dir, ".trusted-ca-bundle-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, target); err != nil {
		return "", err
	}
	return target, nil
}

// SetEnv 把合并后的信任链路径写入当前进程环境。
// 子进程通过 SSL_CERT_FILE 读取；同时兼容 curl 与 Python requests 的变量名。
func SetEnv(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	for _, key := range []string{"SSL_CERT_FILE", "CURL_CA_BUNDLE", "REQUESTS_CA_BUNDLE"} {
		if err := os.Setenv(key, path); err != nil {
			return err
		}
	}
	return nil
}
