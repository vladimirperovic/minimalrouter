//go:build linux

package main

import (
	"fmt"
	"net"
	"os/user"
	"strconv"

	"golang.org/x/sys/unix"
)

func validatePeer(conn net.Conn) error {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("not a Unix connection")
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return err
	}
	var cred *unix.Ucred
	var controlErr error
	if err := raw.Control(func(fd uintptr) {
		cred, controlErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return err
	}
	if controlErr != nil {
		return controlErr
	}
	routerdUser, err := user.Lookup("routerd")
	if err != nil {
		return err
	}
	routerdUID, err := strconv.ParseUint(routerdUser.Uid, 10, 32)
	if err != nil {
		return err
	}
	if cred.Uid != 0 && cred.Uid != uint32(routerdUID) {
		return fmt.Errorf("unexpected peer uid %d", cred.Uid)
	}
	return nil
}
