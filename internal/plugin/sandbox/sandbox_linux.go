//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

type rulesetAttr struct {
	HandledAccessFS uint64
}

type pathBeneathAttr struct {
	AllowedAccess uint64
	ParentFD      int32
}

func landlockABI() (int, error) {
	version, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return 0, errno
	}
	return int(version), nil
}

// Supports 判断当前内核是否支持 Landlock 严格沙箱。
// 只探测 ABI 与 ruleset 创建，不修改调用进程的沙箱状态，可在父进程安全调用。
func Supports() bool {
	abi, err := landlockABI()
	if err != nil {
		return false
	}
	fd, err := createRuleset(landlockAccess(abi))
	if err != nil {
		return false
	}
	_ = unix.Close(fd)
	return true
}

func createRuleset(handled uint64) (int, error) {
	attr := rulesetAttr{HandledAccessFS: handled}
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return -1, errno
	}
	return int(fd), nil
}

func addPathRule(rulesetFD int, path string, access uint64) error {
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	attr := pathBeneathAttr{AllowedAccess: access, ParentFD: int32(fd)}
	_, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(rulesetFD), uintptr(unix.LANDLOCK_RULE_PATH_BENEATH), uintptr(unsafe.Pointer(&attr)), 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

func landlockAccess(abi int) uint64 {
	access := uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE |
		unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_CHAR |
		unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
		unix.LANDLOCK_ACCESS_FS_MAKE_REG |
		unix.LANDLOCK_ACCESS_FS_MAKE_SOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_FIFO |
		unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM)
	if abi >= 2 {
		access |= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi >= 3 {
		access |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	if abi >= 5 {
		access |= unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	}
	return access
}

// Run 应用 Linux strict 沙箱并执行 argv[0]。
func Run(spec Spec, argv []string) error {
	// 父进程应在调用包装器前完成降权；这里再兜底拒绝 root，防止其他调用方绕过。
	if os.Geteuid() == 0 {
		return ErrRoot
	}
	spec = normalizeSpec(spec)
	if err := validateSpec(spec); err != nil {
		return err
	}
	if len(argv) == 0 {
		return fmt.Errorf("sandbox: empty command")
	}

	abi, err := landlockABI()
	if err != nil {
		return fmt.Errorf("%w: landlock ABI: %v", ErrUnsupported, err)
	}
	handled := landlockAccess(abi)
	ruleset, err := createRuleset(handled)
	if err != nil {
		return fmt.Errorf("%w: landlock ruleset: %v", ErrUnsupported, err)
	}
	defer unix.Close(ruleset)
	unix.CloseOnExec(ruleset)

	readAccess := uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR)
	writeAccess := handled

	// 允许遍历根目录，但不能读取任意文件；具体可读路径由下面的规则限定。
	if err := addPathRule(ruleset, "/", uint64(unix.LANDLOCK_ACCESS_FS_READ_DIR)); err != nil {
		return fmt.Errorf("sandbox: root rule: %w", err)
	}
	defaultReadOnly := []string{
		"/usr", "/lib", "/lib64", "/bin", "/sbin", "/etc/ld.so.cache",
		"/etc/ssl", "/etc/pki", "/etc/ca-certificates",
		"/etc/resolv.conf", "/etc/hosts", "/etc/nsswitch.conf", "/etc/localtime",
		"/etc/passwd", "/etc/group", "/etc/timezone",
		"/usr/share/zoneinfo", "/proc/self", "/sys/kernel/mm/transparent_hugepage",
		"/dev/null", "/dev/urandom", "/dev/random",
	}
	for _, path := range append(defaultReadOnly, spec.ReadOnlyPaths...) {
		info, statErr := os.Stat(path)
		if statErr != nil {
			continue
		}
		access := readAccess
		if !info.IsDir() {
			// Landlock 不允许对普通文件声明 READ_DIR；否则 add_rule 返回 EINVAL。
			access &^= uint64(unix.LANDLOCK_ACCESS_FS_READ_DIR)
		}
		if err := addPathRule(ruleset, path, access); err != nil {
			return fmt.Errorf("sandbox: read-only %s: %w", path, err)
		}
	}
	// /dev/null 等字符设备常被 shell/日志库写入，单独允许读写。
	for _, path := range []string{"/dev/null", "/dev/zero"} {
		if _, statErr := os.Stat(path); statErr == nil {
			if err := addPathRule(ruleset, path, uint64(unix.LANDLOCK_ACCESS_FS_READ_FILE|unix.LANDLOCK_ACCESS_FS_WRITE_FILE)); err != nil {
				return fmt.Errorf("sandbox: device %s: %w", path, err)
			}
		}
	}
	if err := addPathRule(ruleset, spec.WritableDir, writeAccess); err != nil {
		return fmt.Errorf("sandbox: writable %s: %w", spec.WritableDir, err)
	}
	helperAccess := readAccess
	if info, statErr := os.Stat(argv[0]); statErr == nil && !info.IsDir() {
		helperAccess &^= uint64(unix.LANDLOCK_ACCESS_FS_READ_DIR)
	}
	if err := addPathRule(ruleset, argv[0], helperAccess); err != nil {
		return fmt.Errorf("sandbox: helper %s: %w", argv[0], err)
	}

	// Landlock restrict_self 在非 root 下要求先设置 no_new_privs。
	if _, _, errno := unix.Syscall6(unix.SYS_PRCTL, unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0, 0); errno != 0 {
		return fmt.Errorf("sandbox: no_new_privs: %v", errno)
	}
	if _, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, uintptr(ruleset), 0, 0); errno != 0 {
		return fmt.Errorf("%w: landlock restrict: %v", ErrUnsupported, errno)
	}
	if err := applyLimits(spec); err != nil {
		return err
	}
	_ = unix.Umask(0o077)
	if err := os.Chdir(spec.WritableDir); err != nil {
		return fmt.Errorf("sandbox: chdir: %w", err)
	}
	return syscall.Exec(argv[0], argv, os.Environ())
}

func applyLimits(spec Spec) error {
	if spec.Limits.CPUSeconds > 0 {
		limit := &unix.Rlimit{Cur: spec.Limits.CPUSeconds, Max: spec.Limits.CPUSeconds}
		if err := unix.Prlimit(0, unix.RLIMIT_CPU, limit, nil); err != nil {
			return fmt.Errorf("sandbox: rlimit cpu: %w", err)
		}
	}
	if spec.Limits.FileSizeMB > 0 {
		bytes := spec.Limits.FileSizeMB << 20
		limit := &unix.Rlimit{Cur: bytes, Max: bytes}
		if err := unix.Prlimit(0, unix.RLIMIT_FSIZE, limit, nil); err != nil {
			return fmt.Errorf("sandbox: rlimit fsize: %w", err)
		}
	}
	if spec.Limits.OpenFiles > 0 {
		limit := &unix.Rlimit{Cur: spec.Limits.OpenFiles, Max: spec.Limits.OpenFiles}
		if err := unix.Prlimit(0, unix.RLIMIT_NOFILE, limit, nil); err != nil {
			return fmt.Errorf("sandbox: rlimit nofile: %w", err)
		}
	}
	if spec.Limits.Processes > 0 {
		limit := &unix.Rlimit{Cur: spec.Limits.Processes, Max: spec.Limits.Processes}
		if err := unix.Prlimit(0, unix.RLIMIT_NPROC, limit, nil); err != nil {
			return fmt.Errorf("sandbox: rlimit nproc: %w", err)
		}
	}
	return nil
}
