//go:build unix

package faultinject

import (
	"fmt"
	"os"
	"syscall"
)

func ownedByRootOrSelf(path string, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%s ownership is unknown", path)
	}
	if stat.Uid != 0 && int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("%s is owned by uid %d", path, stat.Uid)
	}
	return nil
}
