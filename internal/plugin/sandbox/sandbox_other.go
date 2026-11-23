//go:build !linux

package sandbox

import "fmt"

// Run 在非 Linux 平台返回不支持；宿主会降级为 basic 资源限制。
func Run(spec Spec, argv []string) error {
	return fmt.Errorf("%w: strict sandbox requires Linux", ErrUnsupported)
}

// Supports 在非 Linux 平台始终返回 false。
func Supports() bool { return false }
