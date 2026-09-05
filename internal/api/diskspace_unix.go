//go:build unix

package api

import "golang.org/x/sys/unix"

// availableBytes reports the space a normal process may still use on the
// filesystem holding path. An update must not start unless the download, the
// expanded payload and the new slot all fit while current and previous stay
// intact: running the appliance out of space mid-update is exactly the
// situation A/B slots exist to avoid.
func availableBytes(path string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}
