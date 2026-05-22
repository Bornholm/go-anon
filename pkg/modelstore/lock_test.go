//go:build !windows

package modelstore

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestLockAcquireRelease(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".lock-test")

	lock, err := acquireLock(lockPath)
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}

	if err := lock.Release(); err != nil {
		t.Fatalf("release lock: %v", err)
	}

	info, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal("lock file should exist after release")
	}
	if info.Size() != 0 {
		t.Error("lock file should be empty")
	}
}

func TestLockExclusive(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".lock-test")

	lock1, err := acquireLock(lockPath)
	if err != nil {
		t.Fatalf("acquire lock1: %v", err)
	}

	done := make(chan bool, 1)
	go func() {
		lock2, err := acquireLock(lockPath)
		if err != nil {
			t.Errorf("acquire lock2: %v", err)
			done <- false
			return
		}
		lock2.Release()
		done <- true
	}()

	select {
	case <-done:
		t.Fatal("lock2 should be blocked")
	default:
	}

	if err := lock1.Release(); err != nil {
		t.Fatalf("release lock1: %v", err)
	}

	select {
	case success := <-done:
		if !success {
			t.Fatal("lock2 failed to acquire after lock1 released")
		}
	}
}

func TestLockConcurrent(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".lock-test")

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lock, err := acquireLock(lockPath)
			if err != nil {
				t.Errorf("acquire lock: %v", err)
				return
			}
			defer lock.Release()
		}()
	}

	wg.Wait()
}

func TestLockDoubleRelease(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".lock-test")

	lock, err := acquireLock(lockPath)
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}

	if err := lock.Release(); err != nil {
		t.Fatalf("release lock: %v", err)
	}

	if err := lock.Release(); err != nil {
		t.Fatalf("double release should not error: %v", err)
	}
}
