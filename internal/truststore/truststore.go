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
	"/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem",
	"/etc/ssl/certs/ca-bundle.crt",
	"/etc/ssl/ca-bundle.pem",
	"/etc/pki/tls/cacert.pem",
	"/etc/ssl/cacert.pem",
	"/etc/ssl/cert.pem",
	"/usr/local/etc/ssl/cert.pem",
	"/usr/local/share/certs/ca-root-nss.crt",
}

// systemBundleDirs 是找不到合并文件时逐个读取 PEM 的目录。
// 某些精简发行版只提供散装证书，不生成 ca-certificates.crt。
var systemBundleDirs = []string{
	"/etc/ssl/certs",
	"/etc/pki/tls/certs",
	"/usr/local/etc/ssl/certs",
	"/usr/local/share/certs",
}

var (
	systemOnce sync.Once
	systemPEM  []byte

	bundleMu   sync.RWMutex
	bundlePath string
)

// SystemPEM 返回操作系统根证书的 PEM 内容。
// 优先使用未被本程序接管的 SSL_CERT_FILE，其次读取常见发行版路径；
// 仍找不到时再读取系统证书目录，兼容只安装散装 PEM 的精简系统。
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
		dirs := make([]string, 0, len(systemBundleDirs)+1)
		if custom := strings.TrimSpace(os.Getenv("SSL_CERT_DIR")); custom != "" {
			dirs = append(dirs, filepath.SplitList(custom)...)
		}
		dirs = append(dirs, systemBundleDirs...)
		for _, dir := range dirs {
			if data := readCertDir(dir); len(data) > 0 {
				systemPEM = data
				return
			}
		}
		// Windows 的系统根证书不在文件系统中，需要从证书存储导出。
		systemPEM = platformSystemPEM()
	})
	return systemPEM
}

// readCertDir 读取目录下的 PEM/CRT 文件并拼接为一份证书链。
// 只读取包含 CERTIFICATE 块的文件，避免把私钥或无关配置带进信任链。
func readCertDir(dir string) []byte {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var buf bytes.Buffer
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(entry.Name())) {
		case ".pem", ".crt", ".cer":
		default:
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil || len(bytes.TrimSpace(data)) == 0 || !bytes.Contains(data, []byte("BEGIN CERTIFICATE")) {
			continue
		}
		buf.Write(data)
		if !bytes.HasSuffix(data, []byte("\n")) {
			buf.WriteByte('\n')
		}
	}
	if buf.Len() == 0 {
		return nil
	}
	return buf.Bytes()
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
	return absPath(target), nil
}

// BundlePath 返回当前进程正在使用的信任链文件路径。
// 插件子进程通过 SSL_CERT_FILE 读取；该值也用于在环境变量丢失时兜底。
func BundlePath() string {
	bundleMu.RLock()
	defer bundleMu.RUnlock()
	return bundlePath
}

// SetEnv 把合并后的信任链路径写入当前进程环境。
// 子进程通过 SSL_CERT_FILE 读取；同时兼容 curl 与 Python requests 的变量名。
func SetEnv(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	path = absPath(path)
	for _, key := range []string{"SSL_CERT_FILE", "CURL_CA_BUNDLE", "REQUESTS_CA_BUNDLE"} {
		if err := os.Setenv(key, path); err != nil {
			return err
		}
	}
	bundleMu.Lock()
	bundlePath = path
	bundleMu.Unlock()
	return nil
}

// absPath 把相对路径转换为绝对路径，避免子进程工作目录变化后找不到信任链。
func absPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}
