//go:build !linux

package main

import (
	"errors"
	"net"
)

func validatePeer(_ net.Conn) error {
	return errors.New("router-applyd peer authentication is supported only on Linux")
}
