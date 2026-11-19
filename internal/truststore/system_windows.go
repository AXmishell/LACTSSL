//go:build windows

package truststore

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"unsafe"

	"golang.org/x/sys/windows"
)

// platformSystemPEM 从 Windows 证书存储导出根证书与中间证书。
// Go 在 Windows 上默认读取系统证书存储；生成合并信任链后，插件子进程
// 通过 SSL_CERT_FILE 也能同时信任系统根证书与管理员导入的内部 CA。
func platformSystemPEM() []byte {
	var buf bytes.Buffer
	seen := make(map[string]bool)
	for _, name := range []string{"ROOT", "CA"} {
		store, err := windows.CertOpenSystemStore(0, windows.StringToUTF16Ptr(name))
		if err != nil {
			continue
		}
		var context *windows.CertContext
		for {
			context, err = windows.CertEnumCertificatesInStore(store, context)
			if err != nil || context == nil {
				break
			}
			if context.EncodedCert == nil || context.Length == 0 {
				continue
			}
			der := unsafe.Slice(context.EncodedCert, context.Length)
			key := string(der)
			if seen[key] {
				continue
			}
			seen[key] = true
			cert, err := x509.ParseCertificate(der)
			if err != nil || !cert.IsCA {
				continue
			}
			_ = pem.Encode(&buf, &pem.Block{Type: "CERTIFICATE", Bytes: der})
		}
		_ = windows.CertCloseStore(store, 0)
	}
	return buf.Bytes()
}
