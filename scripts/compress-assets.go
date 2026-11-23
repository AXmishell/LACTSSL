//go:build ignore

// compress-assets 把前端构建产物预压缩为 .gz，并删除原始文件。
//
// 生产构建会把 internal/webui/dist 嵌入二进制；预压缩后只保留压缩
// 表示，可显著减小二进制与运行时文件页。index.html 需要由服务端注入
// 运行时配置，保持原文不压缩。
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
	var rawBytes, gzipBytes int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		name := entry.Name()
		// 已压缩文件不重复处理；index.html 保留原文供服务端注入。
		if strings.HasSuffix(name, ".gz") || name == "index.html" {
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
		gzipBytes += compressed
		return nil
	})
	if err != nil {
		log.Fatalf("压缩前端资源失败: %v", err)
	}
	if files == 0 {
		fmt.Printf("前端资源无需压缩（已处理或全部小于压缩收益）: %s\n", root)
		return
	}
	saved := rawBytes - gzipBytes
	fmt.Printf("前端资源已压缩: %d 个文件, %d -> %d 字节 (%.1f%%), 节省 %.2f MB\n",
		files, rawBytes, gzipBytes, float64(gzipBytes)*100/float64(rawBytes), float64(saved)/(1<<20))
}

// compressFile 把 path 压缩为 path+".gz"，成功且更小时删除原文件。
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

	zw, err := gzip.NewWriterLevel(tmp, gzip.BestCompression)
	if err != nil {
		cleanup()
		return 0, 0, false, err
	}
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
		_ = os.Remove(path + ".gz")
		return info.Size(), outInfo.Size(), false, nil
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		_ = os.Remove(tmpPath)
		return 0, 0, false, err
	}
	// Windows 上 Rename 不会覆盖已存在的目标，先删除旧产物。
	_ = os.Remove(path + ".gz")
	if err := os.Rename(tmpPath, path+".gz"); err != nil {
		_ = os.Remove(tmpPath)
		return 0, 0, false, err
	}
	if err := os.Remove(path); err != nil {
		return 0, 0, false, err
	}
	return info.Size(), outInfo.Size(), true, nil
}
