package bundled

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// FileName 是 helper 写入插件目录后的文件名。
const FileName = "lactssl-dns-helper-extra"

// PathName 返回当前平台使用的 helper 文件名。
func PathName() string {
	if runtime.GOOS == "windows" {
		return FileName + ".exe"
	}
	return FileName
}

// Install 把嵌入的 helper 安装到插件目录，返回实际路径与是否发生写入。
// 未嵌入 helper、目录为空或内容一致时不写入。
func Install(dir string) (string, bool, error) {
	return install(Helper(), dir)
}

func install(data []byte, dir string) (string, bool, error) {
	if len(data) == 0 || strings.TrimSpace(dir) == "" {
		return "", false, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", false, fmt.Errorf("创建插件目录失败: %w", err)
	}
	dest := filepath.Join(dir, PathName())
	if info, err := os.Lstat(dest); err == nil {
		if !info.Mode().IsRegular() {
			return dest, false, fmt.Errorf("插件路径不是普通文件: %s", dest)
		}
		existing, readErr := os.ReadFile(dest)
		if readErr == nil && bytes.Equal(existing, data) {
			return dest, false, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return dest, false, fmt.Errorf("检查插件文件失败: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".bundled-*")
	if err != nil {
		return dest, false, fmt.Errorf("创建临时插件文件失败: %w", err)
	}
	tmpName := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return dest, false, fmt.Errorf("设置插件权限失败: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return dest, false, fmt.Errorf("写入插件失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return dest, false, fmt.Errorf("同步插件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return dest, false, fmt.Errorf("关闭插件失败: %w", err)
	}
	// Windows 不允许 rename 覆盖已存在文件，失败时先删除再重试。
	if err := os.Rename(tmpName, dest); err != nil {
		if removeErr := os.Remove(dest); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return dest, false, fmt.Errorf("替换旧插件失败: %w", err)
		}
		if retryErr := os.Rename(tmpName, dest); retryErr != nil {
			return dest, false, fmt.Errorf("安装插件失败: %w", retryErr)
		}
	}
	removeTemp = false
	return dest, true, nil
}
