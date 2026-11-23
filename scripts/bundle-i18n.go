//go:build ignore

// bundle-i18n 把 i18n 语言包预压缩为 .json.gz，供 embed 构建使用。
//
// 语言包是纯文本 JSON，压缩后可显著减小二进制体积。发布构建会嵌入
// internal/i18n/dist；开发构建（!embed）仍直接读取 locales/*.json。
//
// 用法：go run ./scripts/bundle-i18n.go
package main

import (
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	src := flag.String("src", "internal/i18n/locales", "语言包源目录")
	out := flag.String("out", "internal/i18n/dist", "压缩产物输出目录")
	flag.Parse()

	entries, err := os.ReadDir(*src)
	if err != nil {
		log.Fatalf("读取语言包目录失败: %v", err)
	}
	// 清理旧产物，避免删除语言文件后仍嵌入过期语言包。
	if err := os.RemoveAll(*out); err != nil {
		log.Fatalf("清理输出目录失败: %v", err)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatalf("创建输出目录失败: %v", err)
	}

	var files int
	var rawBytes, gzipBytes int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		srcPath := filepath.Join(*src, entry.Name())
		data, err := os.ReadFile(srcPath)
		if err != nil {
			log.Fatalf("读取 %s 失败: %v", srcPath, err)
		}

		var buf bytes.Buffer
		zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		if err != nil {
			log.Fatalf("创建压缩器失败: %v", err)
		}
		if _, err := zw.Write(data); err != nil {
			log.Fatalf("压缩 %s 失败: %v", srcPath, err)
		}
		if err := zw.Close(); err != nil {
			log.Fatalf("压缩 %s 失败: %v", srcPath, err)
		}

		outPath := filepath.Join(*out, entry.Name()+".gz")
		if err := os.WriteFile(outPath, buf.Bytes(), 0o644); err != nil {
			log.Fatalf("写入 %s 失败: %v", outPath, err)
		}
		files++
		rawBytes += int64(len(data))
		gzipBytes += int64(buf.Len())
	}

	if files == 0 {
		log.Fatalf("语言包目录中没有 .json 文件: %s", *src)
	}
	fmt.Printf("i18n 语言包已压缩: %d 个文件, %d -> %d 字节 (%.1f%%), 节省 %.2f MB\n",
		files, rawBytes, gzipBytes, float64(gzipBytes)*100/float64(rawBytes), float64(rawBytes-gzipBytes)/(1<<20))
}
