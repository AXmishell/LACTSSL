// Command dbconvert 在服务停止时执行 SQLite、PostgreSQL 与 MySQL 之间的逻辑切换。
// Web 管理端的“在线切换”复用 internal/dbconvert 的同一套核心实现。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"lactssl/internal/config"
	"lactssl/internal/db"
	"lactssl/internal/dbconvert"
	"lactssl/internal/util"
)

func main() {
	var (
		configPath      = flag.String("config", config.DefaultPath, "config.yaml 路径，用于读取 data_dir 与 encryption salt")
		sourceDriver    = flag.String("source-driver", "sqlite", "源数据库驱动：sqlite、postgres 或 mysql")
		source          = flag.String("source", "", "源 SQLite 路径或 PostgreSQL/MySQL DSN")
		targetDriver    = flag.String("target-driver", "postgres", "目标数据库驱动：sqlite、postgres 或 mysql")
		target          = flag.String("target", "", "目标 SQLite 路径或 PostgreSQL/MySQL DSN")
		dataDir         = flag.String("data-dir", "", "数据目录；留空使用配置中的 data_dir")
		force           = flag.Bool("force", false, "目标库非空时清空当前 schema（危险）")
		allowSameDriver = flag.Bool("allow-same-driver", false, "允许同构迁移（SQLite→SQLite、PostgreSQL→PostgreSQL），目标必须为空")
		switchConfig    = flag.Bool("switch", false, "切换成功后改写 config.yaml 的 database 段")
		yes             = flag.Bool("yes", false, "跳过确认")
	)
	flag.Parse()

	from, ok := db.ParseDialect(*sourceDriver)
	if !ok {
		log.Fatalf("不支持的源驱动: %s", *sourceDriver)
	}
	to, ok := db.ParseDialect(*targetDriver)
	if !ok {
		log.Fatalf("不支持的目标驱动: %s", *targetDriver)
	}
	if from == to && !*allowSameDriver {
		log.Fatal("源与目标驱动相同；如需同构迁移请显式加 -allow-same-driver")
	}
	if from == to && *force {
		log.Fatal("同构迁移不允许清空目标库，请使用空目标")
	}
	if strings.TrimSpace(*source) == "" || strings.TrimSpace(*target) == "" {
		log.Fatal("必须提供 -source 与 -target")
	}

	var cfg *config.Config
	var err error
	if *configPath != "" {
		cfg, err = config.LoadFrom(*configPath)
		if err != nil {
			log.Fatalf("加载配置失败: %v", err)
		}
	}
	dir := strings.TrimSpace(*dataDir)
	if dir == "" && cfg != nil {
		dir = cfg.DataDir
	}
	if dir == "" {
		log.Fatal("必须提供 -data-dir 或可读取的 config.yaml")
	}
	var cipher *util.Cipher
	if cfg != nil {
		cipher, err = util.NewCipher(cfg.EncryptionKey)
		if err != nil {
			log.Fatalf("初始化加密组件失败: %v", err)
		}
	}

	if !*yes {
		fmt.Printf("即将把 %s 切换为 %s。目标库非空时将被拒绝%s。\n", from, to, map[bool]string{true: "（已启用 -force 清空）", false: ""}[*force])
		fmt.Print("输入 CONVERT 继续: ")
		var confirm string
		_, _ = fmt.Scanln(&confirm)
		if confirm != "CONVERT" {
			log.Fatal("已取消")
		}
	}

	opts := dbconvert.Options{
		SourceDriver:    from,
		TargetDriver:    to,
		DataDir:         dir,
		ConfigPath:      *configPath,
		Cipher:          cipher,
		Force:           *force,
		AllowSameDriver: *allowSameDriver,
	}
	if from == db.SQLite {
		opts.SourcePath = *source
	} else {
		opts.SourceDSN = *source
	}
	if to == db.SQLite {
		opts.TargetPath = *target
	} else {
		opts.TargetDSN = *target
	}
	runner := dbconvert.NewRunner(opts)
	report, err := runner.Run(context.Background())
	if err != nil {
		log.Fatalf("切换失败: %v", err)
	}
	fmt.Printf("切换完成：%d 张表，%d 行，备份 %s\n", len(report.Tables), report.Rows, report.BackupPath)

	if *switchConfig {
		sqlitePath := ""
		dsn := ""
		if to == db.SQLite {
			sqlitePath = opts.TargetPath
		} else {
			dsn = opts.TargetDSN
		}
		backup, err := config.RewriteDatabase(*configPath, to.String(), dsn, sqlitePath)
		if err != nil {
			log.Fatalf("改写配置失败: %v", err)
		}
		state := &dbconvert.State{
			ID:            time.Now().UTC().Format("20060102-150405"),
			Phase:         dbconvert.PhaseSwitching,
			FromDriver:    from.String(),
			ToDriver:      to.String(),
			FromPath:      opts.SourcePath,
			ToPath:        opts.TargetPath,
			ToDSNSummary:  dbconvert.DSNSummary(opts.TargetDSN),
			BackupPath:    report.BackupPath,
			ConfigBackup:  backup,
			TargetCreated: true,
			StartedAt:     report.StartedAt,
		}
		if err := dbconvert.SaveState(dir, state); err != nil {
			log.Fatalf("写入切换状态失败: %v", err)
		}
		fmt.Printf("已改写配置 %s（旧配置备份 %s），请重启服务。\n", *configPath, backup)
	}
	_ = os.Stdout.Sync()
}
