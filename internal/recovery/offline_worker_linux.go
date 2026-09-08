//go:build linux

package recovery

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

func migrationWorkerIdentity() (uint32, uint32, error) {
	account, err := user.Lookup("routerd")
	if err != nil {
		return 0, 0, fmt.Errorf("resolve fixed routerd worker account: %w", err)
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil || uid == 0 {
		return 0, 0, errors.New("migration worker requires a non-root routerd UID")
	}
	gid, err := strconv.ParseUint(account.Gid, 10, 32)
	if err != nil || gid == 0 {
		return 0, 0, errors.New("migration worker requires a non-root routerd GID")
	}
	return uint32(uid), uint32(gid), nil
}

func openMigrationDBWorker(dir string) (migrationDB, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("only root may coordinate offline migration")
	}
	uid, gid, err := migrationWorkerIdentity()
	if err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	var stat unix.Stat_t
	if err := unix.Lstat(executable, &stat); err != nil {
		return nil, err
	}
	if stat.Uid != 0 || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0022 != 0 || stat.Mode&(unix.S_ISUID|unix.S_ISGID) != 0 {
		return nil, errors.New("migration worker executable must be root-owned, non-writable, and without set-ID bits")
	}
	cmd := exec.Command(executable, MigrationDBWorkerArgument)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin"}
	cmd.Dir = "/"
	cmd.SysProcAttr = migrationWorkerCredentials(uid, gid)
	return openWorkerProcess(cmd, dir)
}

func migrationWorkerCredentials(uid, gid uint32) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Credential:  &syscall.Credential{Uid: uid, Gid: gid, Groups: []uint32{}},
		AmbientCaps: []uintptr{},
	}
}

func hardenMigrationDBWorker() error {
	uid, gid, err := migrationWorkerIdentity()
	if err != nil {
		return err
	}
	return restrictMigrationDBWorker(uid, gid)
}

func restrictMigrationDBWorker(uid, gid uint32) error {
	// Database calls remain on this thread after no_new_privs. All runtime
	// threads already inherited the permanent IDs and zero capabilities at exec.
	runtime.LockOSThread()
	r, e, s := unix.Getresuid()
	gr, ge, gs := unix.Getresgid()
	// Compare in a signed width that represents every uint32 ID without
	// truncation, including on 32-bit hosts; negative syscall values must fail.
	if uid == 0 || gid == 0 || int64(r) != int64(uid) || int64(e) != int64(uid) || int64(s) != int64(uid) || int64(gr) != int64(gid) || int64(ge) != int64(gid) || int64(gs) != int64(gid) {
		return errors.New("migration database worker must have permanently dropped all user/group IDs")
	}
	groups, err := unix.Getgroups()
	if err != nil || len(groups) != 0 {
		return errors.New("migration database worker must have no supplementary groups")
	}
	// Refuse unexpected file/inherited capabilities rather than accepting a
	// privileged executable and clearing only one already-running Go thread.
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var caps [2]unix.CapUserData
	if err := unix.Capget(&header, &caps[0]); err != nil {
		return err
	}
	if caps[0] != (unix.CapUserData{}) || caps[1] != (unix.CapUserData{}) {
		return errors.New("migration database worker inherited capabilities")
	}
	if err := unix.Capset(&header, &caps[0]); err != nil {
		return err
	}
	if err := unix.Prctl(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0); err != nil {
		return err
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return err
	}
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return err
	}
	if err := unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{}); err != nil {
		return err
	}
	unix.Umask(0077)
	return nil
}
