//go:build ignore

// compress-assets 把前端构建产物预压缩为 .br，并删除原始文件。
//
// 生产构建会把 internal/webui/dist 嵌入二进制；预压缩后只保留 Brotli
// 表示，可显著减小二进制与运行时文件页。index.html 需要由服务端注入
// 运行时配置，保持原文不压缩。
//
// Brotli 相比 gzip 对 JS/CSS 通常再小 15%~25%，现代浏览器均支持。
// 不支持 br 的客户端由服务端流式解压，无需保留 gzip 副本。
//
// 用法：go run ./scripts/compress-assets.go internal/webui/dist
package main

import (
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/andybalholm/brotli"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "用法: go run %s <dist-dir>\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	root := flag.Arg(0)

	var files int
	var rawBytes, brBytes int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		name := entry.Name()
		// 已压缩文件不重复处理；index.html 保留原文供服务端注入。
		if strings.HasSuffix(name, ".br") || name == "index.html" {
			return nil
		}
		// 旧版 gzip 产物不再使用：有原始文件时直接删除，只剩 .gz 时先还原
		// 再转成 .br，避免在旧构建目录上直接运行本脚本导致资源丢失。
		if strings.HasSuffix(name, ".gz") {
			rawPath := strings.TrimSuffix(path, ".gz")
			if _, err := os.Stat(rawPath); err == nil {
				return os.Remove(path)
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			size, compressed, ok, err := convertGzip(path, rawPath, info.Mode().Perm())
			if err != nil {
				return err
			}
			if ok {
				files++
				rawBytes += size
				brBytes += compressed
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		size, compressed, ok, err := compressFile(path, info.Mode().Perm())
		if err != nil {
			return err
		}
		if !ok {
			// 压缩后反而更大时保留原文件，由服务端直接返回。
			return nil
		}
		files++
		rawBytes += size
		brBytes += compressed
		return nil
	})
	if err != nil {
		log.Fatalf("压缩前端资源失败: %v", err)
	}
	if files == 0 {
		fmt.Printf("前端资源无需压缩（已处理或全部小于压缩收益）: %s\n", root)
		return
	}
	saved := rawBytes - brBytes
	fmt.Printf("前端资源已压缩: %d 个文件, %d -> %d 字节 (%.1f%%), 节省 %.2f MB\n",
		files, rawBytes, brBytes, float64(brBytes)*100/float64(rawBytes), float64(saved)/(1<<20))
}

// convertGzip 把旧版 gzip 产物还原为原始文件，再调用 compressFile 转成 .br。
// 仅用于兼容在旧构建目录上直接运行本脚本的情况。
func convertGzip(path, rawPath string, mode os.FileMode) (int64, int64, bool, error) {
	src, err := os.Open(path)
	if err != nil {
		return 0, 0, false, err
	}
	defer src.Close()
	zr, err := gzip.NewReader(src)
	if err != nil {
		return 0, 0, false, err
	}
	defer zr.Close()

	tmp, err := os.CreateTemp(filepath.Dir(rawPath), ".decompress-*")
	if err != nil {
		return 0, 0, false, err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if _, err := io.Copy(tmp, zr); err != nil {
		cleanup()
		return 0, 0, false, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return 0, 0, false, err
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		_ = os.Remove(tmpPath)
		return 0, 0, false, err
	}
	if err := os.Rename(tmpPath, rawPath); err != nil {
		_ = os.Remove(tmpPath)
		return 0, 0, false, err
	}

	size, compressed, ok, err := compressFile(rawPath, mode)
	if err != nil {
		return 0, 0, false, err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return 0, 0, false, err
	}
	return size, compressed, ok, nil
}

// compressFile 把 path 压缩为 path+".br"，成功且更小时删除原文件。
// 返回原始大小、压缩大小以及是否采用了压缩结果。
func compressFile(path string, mode os.FileMode) (int64, int64, bool, error) {
	src, err := os.Open(path)
	if err != nil {
		return 0, 0, false, err
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return 0, 0, false, err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".compress-*")
	if err != nil {
		return 0, 0, false, err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}

	// 构建期一次性压缩，使用最高质量换取最小体积。
	zw := brotli.NewWriterLevel(tmp, brotli.BestCompression)
	if _, err := io.Copy(zw, src); err != nil {
		cleanup()
		return 0, 0, false, err
	}
	if err := zw.Close(); err != nil {
		cleanup()
		return 0, 0, false, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return 0, 0, false, err
	}

	outInfo, err := os.Stat(tmpPath)
	if err != nil {
		_ = os.Remove(tmpPath)
		return 0, 0, false, err
	}
	if outInfo.Size() >= info.Size() {
		_ = os.Remove(tmpPath)
		// 清理可能残留的旧压缩产物，避免服务端优先命中过期内容。
		_ = os.Remove(path + ".br")
		return info.Size(), outInfo.Size(), false, nil
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		_ = os.Remove(tmpPath)
		return 0, 0, false, err
	}
	// Windows 上 Rename 不会覆盖已存在的目标，先删除旧产物。
	_ = os.Remove(path + ".br")
	if err := os.Rename(tmpPath, path+".br"); err != nil {
		_ = os.Remove(tmpPath)
		return 0, 0, false, err
	}
	if err := os.Remove(path); err != nil {
		return 0, 0, false, err
	}
	return info.Size(), outInfo.Size(), true, nil
}
