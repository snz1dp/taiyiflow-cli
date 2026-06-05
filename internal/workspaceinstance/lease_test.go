package workspaceinstance

import (
	"path/filepath"
	"testing"
)

func TestAcquirePersistsInstanceIDAcrossReacquire(t *testing.T) {
	workdir := t.TempDir()

	lease, err := Acquire(workdir)
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}
	firstInstanceID := lease.InstanceID
	if firstInstanceID == "" {
		t.Fatal("expected persisted instance id")
	}
	statePath := filepath.Join(workdir, workspaceStateDirName, instanceStateFileName)
	if err := lease.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	reacquired, err := Acquire(workdir)
	if err != nil {
		t.Fatalf("Acquire after release failed: %v", err)
	}
	defer func() {
		if closeErr := reacquired.Close(); closeErr != nil {
			t.Fatalf("Close reacquired lease failed: %v", closeErr)
		}
	}()
	if reacquired.InstanceID != firstInstanceID {
		t.Fatalf("expected instance id %q to persist via %s, got %q", firstInstanceID, statePath, reacquired.InstanceID)
	}
}

func TestAcquireRejectsConcurrentLeaseForSameWorkspace(t *testing.T) {
	workdir := t.TempDir()

	lease, err := Acquire(workdir)
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}
	defer func() {
		if closeErr := lease.Close(); closeErr != nil {
			t.Fatalf("Close failed: %v", closeErr)
		}
	}()

	secondLease, err := Acquire(workdir)
	if err == nil {
		_ = secondLease.Close()
		t.Fatal("expected concurrent Acquire to fail")
	}
}