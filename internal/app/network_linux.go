//go:build linux

package app

import "syscall"

func bindDevice(iface string) func(string, string, syscall.RawConn) error {
	return func(_, _ string, c syscall.RawConn) error {
		if iface == "" {
			return nil
		}
		var err error
		controlErr := c.Control(func(fd uintptr) {
			err = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, iface)
		})
		if controlErr != nil {
			return controlErr
		}
		return err
	}
}
