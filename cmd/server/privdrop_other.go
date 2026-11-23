//go:build !unix

package main

// dropPrivileges 在非 Unix 平台上是空操作。
func dropPrivileges() error { return nil }
