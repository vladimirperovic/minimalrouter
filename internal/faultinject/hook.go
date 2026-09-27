// Package faultinject provides lab-only transaction hooks for power-loss
// testing. The hooks are armed by writing a shell command into
// $MINIMALROUTER_FAULT_HOOK_DIR/<phase>; routerd and router-applyd then block
// on that command at the corresponding transaction phase, giving an external
// torture-lab runner a deterministic window to hard-stop the VM (qm stop).
//
// The package is a no-op unless MINIMALROUTER_FAULT_HOOK_DIR is set, and hook
// failures are logged and ignored so lab tooling can never corrupt a
// transaction on a production appliance.
package faultinject

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// HookDirEnv selects the hook directory. Empty disables all hooks.
const HookDirEnv = "MINIMALROUTER_FAULT_HOOK_DIR"

// Transaction phases. The order in which they fire for a routine save is:
//
//	PrePrivilegedApply      routerd, before the privileged OpApplyAll RPC
//	PostProvisionalApply    applyd, runtime active + verified, before persistence
//	PreSQLiteCommit         routerd, before the canonical SQLite commit
//	PostSQLiteCommit        routerd, after the canonical SQLite commit, before ack
//	PreCanonicalAck         routerd, before the helper last-good ack (OpCommitConfirmed)
//	DuringFinalReconcile    routerd, during the final runtime reconcile (OpReconcile)
const (
	PrePrivilegedApply   = "pre-privileged-apply"
	PostProvisionalApply = "post-provisional-apply"
	PreSQLiteCommit      = "pre-sqlite-commit"
	PostSQLiteCommit     = "post-sqlite-commit"
	PreCanonicalAck      = "pre-canonical-ack"
	DuringFinalReconcile = "during-final-reconcile"
)

// hookTimeout bounds a blocking hook (e.g. `sleep 300`) so a stale armed hook
// can never hang the appliance forever.
const hookTimeout = 10 * time.Minute

// Run blocks on the fault-injection hook armed for phase, if any. It is safe
// to call from any transaction path: without an armed hook it returns
// immediately.
func Run(phase string) {
	dir := os.Getenv(HookDirEnv)
	if dir == "" {
		return
	}
	path := filepath.Join(dir, phase)
	info, err := os.Lstat(path)
	if err != nil {
		return
	}
	// router-applyd runs the hook through /bin/sh as root. Only a hook that
	// nobody but root (or this process's own user) could have written may
	// run, so a misplaced lab directory cannot become a privilege bridge.
	if err := hookSourceTrusted(dir, path, info); err != nil {
		log.Printf("faultinject: hook %q ignored: %v", phase, err)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	command := strings.TrimSpace(string(data))
	if command == "" {
		return
	}
	log.Printf("faultinject: hook %q armed: %s", phase, command)
	ctx, cancel := context.WithTimeout(context.Background(), hookTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	out, err := cmd.CombinedOutput()
	if err != nil && ctx.Err() == nil {
		log.Printf("faultinject: hook %q failed (continuing): %v", phase, err)
	}
	if len(out) > 0 {
		log.Printf("faultinject: hook %q output: %s", phase, strings.TrimSpace(string(out)))
	}
}

// hookSourceTrusted accepts a regular hook file whose file and directory are
// owned by root or the current user and writable by nobody else.
func hookSourceTrusted(dir, path string, info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	dirInfo, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !dirInfo.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	for _, item := range []struct {
		path string
		info os.FileInfo
	}{{dir, dirInfo}, {path, info}} {
		if item.info.Mode().Perm()&0o022 != 0 {
			return fmt.Errorf("%s is writable by group or others", item.path)
		}
		if err := ownedByRootOrSelf(item.path, item.info); err != nil {
			return err
		}
	}
	return nil
}
