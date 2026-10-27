//go:build !linux

package app

import "syscall"

func bindDevice(string) func(string, string, syscall.RawConn) error { return nil }
