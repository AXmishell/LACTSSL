package pluginrepo

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"lactssl/internal/i18n"
)

const (
	maxExtractedBytes = 512 << 20
	maxPackageEntries = 256
	maxPackageIcons   = 64
	maxMetadataBytes  = 1 << 20
)

// ExtractPackage 安全解包 tar.gz，返回 plugin.json 与可执行文件路径。
func ExtractPackage(archivePath, destDir string) (Metadata, string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return Metadata{}, "", err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return Metadata{}, "", i18n.E("plugin.package.not_targz", "error", err.Error())
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	var total int64
	var metadata Metadata
	var entrypoint string
	seen := map[string]bool{}
	iconData := map[string][]byte{}
	entries := 0
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Metadata{}, "", err
		}
		name := filepath.ToSlash(filepath.Clean(strings.ReplaceAll(header.Name, "\\", "/")))
		if name == "." || name == ".." || strings.HasPrefix(name, "../") || strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
			return Metadata{}, "", fmt.Errorf("插件包包含非法路径: %s", header.Name)
		}
		iconID, iconExt, isIcon := ParseIconPath(name)
		if !isIcon && strings.Contains(name, "/") {
			return Metadata{}, "", fmt.Errorf("插件包包含非法路径: %s", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeReg:
			// 普通文件，继续处理。
		case tar.TypeDir, tar.TypeXGlobalHeader, tar.TypeXHeader, tar.TypeGNULongName, tar.TypeGNULongLink:
			// 目录与 tar 元数据条目直接跳过，兼容常见打包工具。
			continue
		default:
			return Metadata{}, "", fmt.Errorf("插件包包含非普通文件: %s", header.Name)
		}
		if seen[name] {
			return Metadata{}, "", fmt.Errorf("插件包包含重复文件: %s", name)
		}
		seen[name] = true
		entries++
		if entries > maxPackageEntries {
			return Metadata{}, "", errors.New("插件包条目数量超过限制")
		}
		total += header.Size
		if total > maxExtractedBytes {
			return Metadata{}, "", errors.New("插件包解压后超过大小限制")
		}
		switch {
		case name == "plugin.json":
			data, err := io.ReadAll(io.LimitReader(reader, maxMetadataBytes+1))
			if err != nil {
				return Metadata{}, "", err
			}
			if int64(len(data)) != header.Size || len(data) > maxMetadataBytes {
				return Metadata{}, "", fmt.Errorf("插件包文件大小不符: %s", name)
			}
			if err := json.Unmarshal(data, &metadata); err != nil {
				return Metadata{}, "", fmt.Errorf("解析 plugin.json 失败: %w", err)
			}
		case name == "LICENSE":
			// 仅用于发布包说明，不安装；由 tar.Reader 自动跳过内容。
		case isIcon:
			if _, dup := iconData[iconID]; dup {
				return Metadata{}, "", fmt.Errorf("插件包包含重复图标: %s", iconID)
			}
			if len(iconData) >= maxPackageIcons {
				return Metadata{}, "", errors.New("插件包图标数量超过限制")
			}
			data, err := io.ReadAll(io.LimitReader(reader, MaxIconBytes+1))
			if err != nil {
				return Metadata{}, "", err
			}
			if int64(len(data)) != header.Size || len(data) == 0 || len(data) > MaxIconBytes {
				return Metadata{}, "", fmt.Errorf("插件图标大小非法: %s", name)
			}
			iconData[iconID] = data
			iconDir := filepath.Join(destDir, "icons")
			if err := os.MkdirAll(iconDir, 0o700); err != nil {
				return Metadata{}, "", err
			}
			if err := os.WriteFile(filepath.Join(iconDir, iconID+iconExt), data, 0o644); err != nil {
				return Metadata{}, "", err
			}
		default:
			if entrypoint != "" {
				return Metadata{}, "", i18n.E("plugin.package.multiple_binaries")
			}
			// 可执行文件流式落盘，避免单个大文件被整体读入内存。
			path := filepath.Join(destDir, name)
			out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
			if err != nil {
				return Metadata{}, "", err
			}
			written, copyErr := io.Copy(out, io.LimitReader(reader, header.Size+1))
			closeErr := out.Close()
			if copyErr != nil {
				_ = os.Remove(path)
				return Metadata{}, "", copyErr
			}
			if closeErr != nil {
				_ = os.Remove(path)
				return Metadata{}, "", closeErr
			}
			if written != header.Size {
				_ = os.Remove(path)
				return Metadata{}, "", fmt.Errorf("插件包文件大小不符: %s", name)
			}
			entrypoint = path
		}
	}
	if metadata.Schema != SchemaVersion {
		return Metadata{}, "", fmt.Errorf("不支持的插件包 schema: %d", metadata.Schema)
	}
	if metadata.Protocol != ProtocolVersion {
		return Metadata{}, "", i18n.E("plugin.repo.protocol_unsupported", "protocol", metadata.Protocol)
	}
	if metadata.Entrypoint == "" {
		return Metadata{}, "", errors.New("plugin.json 缺少 entrypoint")
	}
	expected := filepath.Join(destDir, filepath.Base(metadata.Entrypoint))
	if entrypoint == "" || filepath.Clean(entrypoint) != filepath.Clean(expected) {
		return Metadata{}, "", i18n.E("plugin.package.entrypoint_mismatch")
	}
	if err := validatePackageIcons(metadata.Icons, iconData); err != nil {
		return Metadata{}, "", err
	}
	return metadata, entrypoint, nil
}

// validatePackageIcons 校验 plugin.json 的图标声明与包内文件一一对应。
func validatePackageIcons(refs map[string]IconFile, files map[string][]byte) error {
	if len(refs) != len(files) {
		return i18n.E("plugin.package.icon_mismatch")
	}
	for id, ref := range refs {
		raw, ok := files[id]
		if !ok || !idPattern.MatchString(id) {
			return fmt.Errorf("插件包缺少图标: %s", id)
		}
		ext := filepath.Ext(ref.File)
		if ref.File != "icons/"+id+ext || IconMIME(ext) == "" {
			return fmt.Errorf("插件图标路径非法: %s", ref.File)
		}
		if !strings.EqualFold(ref.SHA256, IconSHA256(raw)) {
			return fmt.Errorf("插件图标摘要不符: %s", id)
		}
	}
	return nil
}
