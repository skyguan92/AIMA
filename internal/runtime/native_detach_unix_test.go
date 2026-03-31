//go:build !windows

package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestNativeDeployCreatesDetachedProcessGroup(t *testing.T) {
	rt := newTestRuntime(t)

	if err := rt.Deploy(context.Background(), &DeployRequest{
		Name:    "detached",
		Engine:  "test",
		Command: []string{"sh", "-c", "sleep 30"},
		Port:    18083,
	}); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	defer rt.Delete(context.Background(), "detached")

	rt.mu.RLock()
	proc := rt.processes["detached"]
	rt.mu.RUnlock()
	if proc == nil {
		t.Fatal("expected in-memory process entry")
	}
	if proc.processGroupID <= 0 {
		t.Fatalf("processGroupID = %d, want > 0", proc.processGroupID)
	}

	pgid, err := syscall.Getpgid(proc.pid)
	if err != nil {
		t.Fatalf("Getpgid(%d): %v", proc.pid, err)
	}
	if pgid != proc.processGroupID {
		t.Fatalf("pgid = %d, want recorded process group %d", pgid, proc.processGroupID)
	}
	if pgid == syscall.Getpgrp() {
		t.Fatalf("pgid = %d, should differ from test process group %d", pgid, syscall.Getpgrp())
	}
}

func TestNativeDeleteKillsDetachedProcessGroupChildren(t *testing.T) {
	rt := newTestRuntime(t)

	if err := rt.Deploy(context.Background(), &DeployRequest{
		Name:    "group-kill",
		Engine:  "test",
		Command: []string{"sh", "-c", "sleep 30 & echo $!; wait"},
		Port:    18084,
	}); err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	rt.mu.RLock()
	proc := rt.processes["group-kill"]
	rt.mu.RUnlock()
	if proc == nil {
		t.Fatal("expected in-memory process entry")
	}

	logPath := filepath.Join(rt.logDir, "group-kill.log")
	var childPID int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(logPath)
		if err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
				childPID = pid
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if childPID == 0 {
		t.Fatalf("failed to capture child pid from %s", logPath)
	}
	if !unixProcessRunning(childPID) {
		t.Fatalf("child pid %d is not alive before delete", childPID)
	}

	if err := rt.Delete(context.Background(), "group-kill"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !unixProcessRunning(childPID) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	if unixProcessRunning(childPID) {
		t.Fatalf("child pid %d should be terminated with its detached process group", childPID)
	}
}

func unixProcessRunning(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil {
		return err != syscall.ESRCH
	}
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return true
	}
	stat := strings.TrimSpace(string(out))
	if stat == "" {
		return false
	}
	return !strings.HasPrefix(stat, "Z")
}
