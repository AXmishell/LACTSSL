package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestGateFreezesWrites(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "gate.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec(`CREATE TABLE t(id INTEGER PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	tx, err := conn.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO t(value) VALUES('held')`); err != nil {
		t.Fatal(err)
	}
	frozen := make(chan error, 1)
	go func() {
		frozen <- conn.Freeze(context.Background())
	}()
	select {
	case err := <-frozen:
		t.Fatalf("存在写事务时不应完成冻结: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-frozen:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("写事务结束后冻结未完成")
	}
	writeDone := make(chan error, 1)
	go func() {
		_, err := conn.Exec(`INSERT INTO t(value) VALUES('blocked')`)
		writeDone <- err
	}()
	select {
	case err := <-writeDone:
		t.Fatalf("冻结期间写入不应完成: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	conn.Unfreeze()
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("解冻后写入未恢复")
	}
}

func TestGateFreezeContextCancel(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "gate-cancel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec(`CREATE TABLE t(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	tx, err := conn.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := conn.Freeze(ctx); err == nil {
		t.Fatal("应返回超时错误")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO t(id) VALUES(1)`); err != nil {
		t.Fatal(err)
	}
}
