package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"lactssl/internal/backup"
	"lactssl/internal/config"
	"lactssl/internal/db"
	"lactssl/internal/dbconvert"
	"lactssl/internal/util"
)

func openConfiguredDB(cfg *config.Config) (*db.Handle, error) {
	return db.OpenWithOptions(db.Options{
		Driver:          db.Dialect(cfg.DBDriver),
		Path:            cfg.DBPath,
		DSN:             cfg.DBDSN,
		MaxOpenConns:    cfg.DBMaxOpen,
		MaxIdleConns:    cfg.DBMaxIdle,
		ConnMaxLifetime: cfg.DBConnMaxLife,
		Migrate:         true,
		SingleInstance:  cfg.DBDriver == "postgres" && !cfg.DBAllowMulti,
	})
}

// rollbackConversion 把配置恢复到切换前，并标记状态。
func rollbackConversion(cfg *config.Config, state *dbconvert.State, cause error) (*config.Config, error) {
	if state == nil || state.ConfigBackup == "" {
		return cfg, fmt.Errorf("切换状态缺少配置备份，无法自动回滚")
	}
	if err := backup.ApplyConfig(cfg.File, state.ConfigBackup); err != nil {
		return cfg, fmt.Errorf("恢复旧配置失败: %w", err)
	}
	restored, err := config.LoadFrom(cfg.File)
	if err != nil {
		return cfg, fmt.Errorf("重新加载旧配置失败: %w", err)
	}
	state.Phase = dbconvert.PhaseRolledBack
	state.Error = cause.Error()
	now := time.Now().UTC()
	state.FinishedAt = &now
	_ = dbconvert.SaveState(restored.DataDir, state)
	log.Printf("数据库切换失败，已恢复旧配置: %v", cause)
	return restored, nil
}

// finalizeConversion 在新进程启动后校验目标库；失败时回滚配置并重新打开源库。
func finalizeConversion(cfg *config.Config, conn *db.Handle, state *dbconvert.State, cipher *util.Cipher) (*config.Config, *db.Handle, error) {
	if state == nil {
		return cfg, conn, nil
	}
	// 已完成切换但尚未清理旧库（例如旧版本升级前完成的切换），启动时补做清理。
	if state.Phase == dbconvert.PhaseCompleted {
		cleanupConvertedSource(cfg.DataDir, cfg.DBDriver, state)
		return cfg, conn, nil
	}
	if state.Phase != dbconvert.PhaseSwitching {
		return cfg, conn, nil
	}
	if err := dbconvert.VerifyStarted(context.Background(), conn, cipher); err != nil {
		_ = conn.Close()
		restored, rollbackErr := rollbackConversion(cfg, state, err)
		if rollbackErr != nil {
			return cfg, nil, fmt.Errorf("目标库自检失败且回滚失败: %v; %w", err, rollbackErr)
		}
		reopened, openErr := openConfiguredDB(restored)
		if openErr != nil {
			return restored, nil, fmt.Errorf("回滚后打开源数据库失败: %w", openErr)
		}
		return restored, reopened, nil
	}
	state.Phase = dbconvert.PhaseCompleted
	now := time.Now().UTC()
	state.FinishedAt = &now
	_ = dbconvert.SaveState(cfg.DataDir, state)
	// 目标库已通过自检，切换成功，删除旧 SQLite 数据库；
	// 切换前的备份目录保持不变，仍可用于恢复或回滚。
	cleanupConvertedSource(cfg.DataDir, cfg.DBDriver, state)
	return cfg, conn, nil
}

// cleanupConvertedSource 删除已完成切换的旧 SQLite 文件。清理失败只记录日志，
// 不阻止服务启动；下次启动会重试。只有当前确实运行在目标 PostgreSQL 上才删除，
// 避免配置被手工改回 SQLite 后误删正在使用的库文件。
func cleanupConvertedSource(dataDir, currentDriver string, state *dbconvert.State) {
	// 只清理 SQLite 源库；PostgreSQL → PostgreSQL 保留原库，供管理员确认后手工清理。
	if state.SourceRemoved || state.FromDriver != db.SQLite.String() || currentDriver != state.ToDriver {
		return
	}
	if err := dbconvert.CleanupSource(dataDir, state); err != nil {
		log.Printf("清理旧 SQLite 数据库失败: %v", err)
		return
	}
	state.SourceRemoved = true
	if err := dbconvert.SaveState(dataDir, state); err != nil {
		log.Printf("记录旧数据库清理状态失败: %v", err)
	}
	if strings.TrimSpace(state.FromPath) != "" {
		log.Printf("旧 SQLite 数据库已删除: %s", state.FromPath)
	}
}
