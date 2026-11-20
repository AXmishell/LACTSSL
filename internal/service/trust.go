package service

import (
	"crypto/x509"
	"time"

	"lactssl/internal/truststore"
)

// TrustedPEMs 返回应加入系统 TLS 信任链的证书：
// 管理员导入的 CA 公钥，以及仍在有效期内的本程序私有 CA 证书。
func (m *Manager) TrustedPEMs() []string {
	out := make([]string, 0, 4)
	now := time.Now()
	if list, err := m.Store.ListTrustedCAs(); err == nil {
		for _, ca := range list {
			if now.Before(ca.NotAfter) {
				out = append(out, ca.CertPEM)
			}
		}
	}
	// 列表接口出于脱敏目的不返回 cert_pem，必须使用专门的信任链查询，
	// 否则私有 CA 永远不会进入 SSL_CERT_FILE / 监控信任池。
	if list, err := m.Store.ListPrivateCATrustCertificates(); err == nil {
		out = append(out, list...)
	}
	return out
}

// TrustedRoots 返回系统根证书与内部 CA 合并后的证书池。
func (m *Manager) TrustedRoots() *x509.CertPool {
	return truststore.CertPool(m.TrustedPEMs())
}

// SyncTrustBundle 把当前信任链写入数据目录，供插件与部署命令通过
// SSL_CERT_FILE 读取。dir 为空时只构建内存中的证书池。
func (m *Manager) SyncTrustBundle(dir string) (string, error) {
	return truststore.SyncBundle(dir, m.TrustedPEMs())
}
