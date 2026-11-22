package main

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestLockInstanceExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")

	release, err := lockInstance(path)
	if err != nil {
		t.Fatalf("first lockInstance: %v", err)
	}

	// A second lock on the same file is refused.
	_, err = lockInstance(path)
	if err == nil {
		t.Fatal("expected lock failure for second instance")
	}
	if !errors.Is(err, errAnotherInstance) {
		t.Errorf("expected errAnotherInstance, got %v", err)
	}

	release()

	// After release, the lock can be taken again.
	release2, err := lockInstance(path)
	if err != nil {
		t.Fatalf("second lockInstance after release: %v", err)
	}
	release2()
}
