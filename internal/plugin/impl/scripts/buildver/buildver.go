// Package buildver 计算插件包输入文件的内容指纹，并据此自动递增补丁版本号。
//
// 主/次版本仍由人工维护；补丁号只在某个合并包的输入内容发生变化时递增，
// 因此无需手工修改版本，也不会因为无关改动触发全量重建。
package buildver

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// HashPaths 计算 root 下给定文件与目录的内容指纹。
//
// 目录会递归包含普通文件，忽略测试文件（*_test.go）与 testdata 目录；
// 结果与输入路径的顺序无关，重复或重叠的路径只计入一次。
func HashPaths(root string, paths []string) (string, error) {
	files := make([]string, 0, len(paths))
	for _, path := range paths {
		if err := collect(root, filepath.ToSlash(path), &files); err != nil {
			return "", err
		}
	}
	sort.Strings(files)

	h := sha256.New()
	last := ""
	for _, rel := range files {
		if rel == last {
			continue
		}
		last = rel
		if err := hashFile(h, root, rel); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func collect(root, rel string, files *[]string) error {
	if ignored(rel) {
		return nil
	}
	full := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		*files = append(*files, rel)
		return nil
	}
	return filepath.WalkDir(full, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		child, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		child = filepath.ToSlash(child)
		if d.IsDir() {
			if ignored(child) {
				return filepath.SkipDir
			}
			return nil
		}
		if ignored(child) {
			return nil
		}
		*files = append(*files, child)
		return nil
	})
}

// ignored 过滤不影响构建产物的文件：测试代码与 testdata 测试数据。
func ignored(rel string) bool {
	if strings.HasSuffix(rel, "_test.go") {
		return true
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "testdata" {
			return true
		}
	}
	return false
}

func hashFile(h hash.Hash, root, rel string) error {
	full := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(full)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(full)
		if err != nil {
			return err
		}
		writeString(h, rel)
		writeString(h, target)
		return nil
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	f, err := os.Open(full)
	if err != nil {
		return err
	}
	defer f.Close()

	writeString(h, rel)
	writeUint64(h, uint64(info.Size()))
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	return nil
}

func writeString(h hash.Hash, s string) {
	writeUint64(h, uint64(len(s)))
	_, _ = io.WriteString(h, s)
}

func writeUint64(h hash.Hash, n uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], n)
	_, _ = h.Write(buf[:])
}

// Next 计算插件包的新版本。
//
//   - 新插件：使用基线版本 <base>.0；
//   - 首次引入内容指纹（已有 plugins.json 没有 source_hash）：保留原版本，避免迁移时误发布；
//   - 主/次版本基线变化：使用新的 <base>.0；
//   - 输入内容变化：补丁号 +1；
//   - 内容未变化：保留原版本。
func Next(prevVersion, prevHash, base, newHash string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "0.0"
	}
	if prevVersion == "" {
		return base + ".0"
	}
	if prevHash == "" {
		return prevVersion
	}
	if majorMinor(prevVersion) != base {
		return base + ".0"
	}
	if prevHash != newHash {
		return bumpPatch(prevVersion)
	}
	return prevVersion
}

func majorMinor(v string) string {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if idx := strings.IndexAny(v, "-+"); idx >= 0 {
		v = v[:idx]
	}
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "." + parts[1]
}

func bumpPatch(v string) string {
	core := strings.TrimPrefix(strings.TrimSpace(v), "v")
	if idx := strings.IndexAny(core, "-+"); idx >= 0 {
		core = core[:idx]
	}
	parts := strings.Split(core, ".")
	for len(parts) < 3 {
		parts = append(parts, "0")
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		patch = 0
	}
	parts[2] = strconv.Itoa(patch + 1)
	return strings.Join(parts[:3], ".")
}
