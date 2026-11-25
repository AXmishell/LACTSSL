// Package common 提供 DNS / 部署 / 通知三类外部 helper 共用的目录扫描逻辑。
//
// 三类插件只在 manifest 结构与元数据注册方式上不同，文件安全检查、摘要校验
// 和错误聚合规则完全一致。把这段流程集中后，修一处边界即可覆盖三类插件。
package common

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lactssl/internal/plugin/runtime/discovery"
)

// Candidate 是通过全部文件检查与 manifest 校验的 helper。
type Candidate[M any] struct {
	Path     string
	Manifest M
	Digest   string
}

// ScanOptions 描述一次目录扫描。M 是各插件类型自己的 manifest 结构。
type ScanOptions[M any] struct {
	Dir             string
	DirLabel        string
	DisabledHelpers []string
	ExpectedDigests map[string]string
	DeclaredSHA256  func(M) string
	Validate        func(M) error
}

// Inspect 探测单个 helper，校验文件摘要与 manifest 声明是否一致。
// 上传接口在落盘前调用，三类插件共用同一套校验顺序。
func Inspect[M any](path string, inspect func(string) (M, error), declaredSHA256 func(M) string, validate func(M) error) (M, string, error) {
	var zero M
	manifest, err := inspect(path)
	if err != nil {
		return zero, "", err
	}
	digest, err := discovery.FileSHA256(path)
	if err != nil {
		return zero, "", err
	}
	if declared := declaredSHA256(manifest); declared != "" && !strings.EqualFold(declared, digest) {
		return zero, "", fmt.Errorf("manifest 声明的 sha256 与文件不一致")
	}
	if err := validate(manifest); err != nil {
		return zero, "", err
	}
	return manifest, digest, nil
}

// Scan 扫描插件目录并返回候选 helper、错误列表和被停用的文件名。
//
// 扫描顺序与失败处理保持稳定：先完整探测，调用方再替换注册表，避免中途
// 失败留下半套插件。符号链接、目录、不可执行文件以及组/其他用户可写的
// 文件一律拒绝加载；仓库安装记录中的摘要先于 manifest 执行校验。
func Scan[M any](opts ScanOptions[M], inspect func(string) (M, error)) ([]Candidate[M], []string, []string) {
	var errs []string
	var disabledFiles []string
	entries, err := os.ReadDir(opts.Dir)
	if err != nil {
		if !os.IsNotExist(err) {
			errs = append(errs, fmt.Sprintf("读取%s失败: %v", opts.DirLabel, err))
		}
		return nil, errs, disabledFiles
	}

	disabledSet := make(map[string]bool, len(opts.DisabledHelpers))
	for _, name := range opts.DisabledHelpers {
		if name = strings.TrimSpace(name); name != "" {
			disabledSet[strings.ToLower(name)] = true
		}
	}

	var candidates []Candidate[M]
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if disabledSet[strings.ToLower(name)] {
			disabledFiles = append(disabledFiles, name)
			continue
		}
		path := filepath.Join(opts.Dir, name)
		// Lstat 拒绝符号链接；组或其他用户可写的文件一律不加载。
		info, err := os.Lstat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if !info.Mode().IsRegular() {
			errs = append(errs, fmt.Sprintf("%s: 不是普通文件", name))
			continue
		}
		if info.Mode()&0o111 == 0 {
			errs = append(errs, fmt.Sprintf("%s: 不是可执行文件", name))
			continue
		}
		if info.Mode()&0o022 != 0 {
			errs = append(errs, fmt.Sprintf("%s: 插件文件允许组或其他用户写入，已拒绝加载", name))
			continue
		}
		// 仓库安装记录是本地完整性锚点：先计算摘要，命中记录后再执行
		// manifest，避免被替换的文件在校验前执行。
		if expected := strings.TrimSpace(opts.ExpectedDigests[name]); expected != "" {
			digest, err := discovery.FileSHA256(path)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", name, err))
				continue
			}
			if !strings.EqualFold(expected, digest) {
				errs = append(errs, fmt.Sprintf("%s: 文件摘要与安装记录不一致，已拒绝加载", name))
				continue
			}
		}
		manifest, err := inspect(path)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		digest, err := discovery.FileSHA256(path)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		if declared := opts.DeclaredSHA256(manifest); declared != "" && !strings.EqualFold(declared, digest) {
			errs = append(errs, fmt.Sprintf("%s: sha256 校验失败", name))
			continue
		}
		if err := opts.Validate(manifest); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		candidates = append(candidates, Candidate[M]{Path: path, Manifest: manifest, Digest: digest})
	}
	return candidates, errs, disabledFiles
}
