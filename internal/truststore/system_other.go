//go:build !windows

package truststore

// platformSystemPEM 在非 Windows 平台没有额外证书来源；
// Unix 系统的根证书已经在 systemBundlePaths / systemBundleDirs 中覆盖。
func platformSystemPEM() []byte {
	return nil
}
