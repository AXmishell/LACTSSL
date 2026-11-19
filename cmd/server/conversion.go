package main

import (
	"context"
	"fmt"
	"log"
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

// rollbackConversion 把配置恢复到转换前，并标记状态。
func rollbackConversion(cfg *config.Config, state *dbconvert.State, cause error) (*config.Config, error) {
	if state == nil || state.ConfigBackup == "" {
		return cfg, fmt.Errorf("转换状态缺少配置备份，无法自动回滚")
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
	log.Printf("数据库转换失败，已恢复旧配置: %v", cause)
	return restored, nil
}

// finalizeConversion 在新进程启动后校验目标库；失败时回滚配置并重新打开源库。
func finalizeConversion(cfg *config.Config, conn *db.Handle, state *dbconvert.State, cipher *util.Cipher) (*config.Config, *db.Handle, error) {
	if state == nil || state.Phase != dbconvert.PhaseSwitching {
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
	return cfg, conn, nil
}
