//go:build linux

package storage

import "golang.org/x/sys/unix"

func Inspect(path string) Status {
	if path == "" {
		return Evaluate(0, 0)
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return Evaluate(0, 0)
	}
	blockSize := uint64(stat.Bsize)
	return Evaluate(stat.Blocks*blockSize, stat.Bavail*blockSize)
}
