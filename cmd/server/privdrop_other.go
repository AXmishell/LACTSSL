//go:build !unix

package main

// dropPrivileges 在非 Unix 平台上是空操作。
func dropPrivileges() error { return nil }

// runningAsRoot 在非 Unix 平台不存在 root 概念。
func runningAsRoot() bool { return false }
