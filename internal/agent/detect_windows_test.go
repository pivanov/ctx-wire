//go:build windows

package agent

import (
	"os"
	"testing"
)

// TestProcSnapshotSeesSelf exercises the real Toolhelp walk on Windows: the
// snapshot must contain the current process, with the parent pid the OS reports
// and a non-empty image name, or the ancestor walk has nothing to match on.
func TestProcSnapshotSeesSelf(t *testing.T) {
	procs := procSnapshot()
	if len(procs) == 0 {
		t.Fatal("empty process snapshot")
	}
	self, ok := procs[os.Getpid()]
	if !ok {
		t.Fatalf("snapshot missing current pid %d", os.Getpid())
	}
	if self.ppid != os.Getppid() {
		t.Errorf("snapshot ppid for self = %d, want %d", self.ppid, os.Getppid())
	}
	if self.cmd == "" {
		t.Error("snapshot image name for self is empty")
	}
}
