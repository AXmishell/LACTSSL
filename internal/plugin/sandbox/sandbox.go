// Package sandbox 为上传插件提供受限执行环境。
//
// Linux 下使用 no_new_privs、rlimit 与 Landlock 文件访问白名单；
// 其他平台由宿主降级为 basic 资源限制。
package sandbox

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrUnsupported 表示当前平台或内核不支持 strict 沙箱。
var ErrUnsupported = errors.New("plugin sandbox is not supported on this platform")

// ErrRoot 表示沙箱拒绝以 root 运行：Landlock 无法约束 root 的 mount/ptrace 等操作。
var ErrRoot = errors.New("refusing to run plugin sandbox as root")

// Spec 描述一次沙箱执行。
type Spec struct {
	// WritableDir 是唯一允许写入的目录（同时作为 cwd/HOME/TMPDIR）。
	WritableDir string
	// ReadOnlyPaths 是额外允许读取/执行的路径。
	ReadOnlyPaths []string
	// Limits 是资源限制。
	Limits Limits
}

// Limits 是 best-effort 资源限制。
type Limits struct {
	CPUSeconds uint64
	FileSizeMB uint64
	OpenFiles  uint64
	Processes  uint64
}

func normalizeSpec(spec Spec) Spec {
	spec.WritableDir = strings.TrimSpace(spec.WritableDir)
	out := make([]string, 0, len(spec.ReadOnlyPaths))
	seen := map[string]bool{}
	for _, path := range spec.ReadOnlyPaths {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	spec.ReadOnlyPaths = out
	return spec
}

func validateSpec(spec Spec) error {
	if spec.WritableDir == "" {
		return fmt.Errorf("sandbox writable directory is empty")
	}
	return nil
}

// RunCommand 解析宿主传入的沙箱参数并执行 helper。
// 参数格式：--rw DIR [--ro PATH]... [--cpu N] [--fsize MB] [--nofile N] [--nproc N] -- HELPER [ARGS...]
func RunCommand(args []string) error {
	spec := Spec{}
	var command []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--":
			command = args[i+1:]
			i = len(args)
		case "--rw":
			if i+1 >= len(args) {
				return fmt.Errorf("sandbox: --rw requires a path")
			}
			spec.WritableDir = args[i+1]
			i++
		case "--ro":
			if i+1 >= len(args) {
				return fmt.Errorf("sandbox: --ro requires a path")
			}
			spec.ReadOnlyPaths = append(spec.ReadOnlyPaths, args[i+1])
			i++
		case "--cpu":
			value, err := parseUintArg(args, i)
			if err != nil {
				return err
			}
			spec.Limits.CPUSeconds = value
			i++
		case "--fsize":
			value, err := parseUintArg(args, i)
			if err != nil {
				return err
			}
			spec.Limits.FileSizeMB = value
			i++
		case "--nofile":
			value, err := parseUintArg(args, i)
			if err != nil {
				return err
			}
			spec.Limits.OpenFiles = value
			i++
		case "--nproc":
			value, err := parseUintArg(args, i)
			if err != nil {
				return err
			}
			spec.Limits.Processes = value
			i++
		default:
			return fmt.Errorf("sandbox: unknown argument %q", args[i])
		}
	}
	if len(command) == 0 {
		return fmt.Errorf("sandbox: missing helper command")
	}
	return Run(spec, command)
}

func parseUintArg(args []string, index int) (uint64, error) {
	if index+1 >= len(args) {
		return 0, fmt.Errorf("sandbox: %s requires a value", args[index])
	}
	value, err := strconv.ParseUint(strings.TrimSpace(args[index+1]), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("sandbox: invalid value for %s: %q", args[index], args[index+1])
	}
	return value, nil
}
