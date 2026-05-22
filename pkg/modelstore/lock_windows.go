//go:build windows

package modelstore

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	modkernel32    = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx = modkernel32.NewProc("LockFileEx")
	procUnlockFile = modkernel32.NewProc("UnlockFile")
)

const (
	lockfileExclusiveLock = 0x00000002
)

type fileLock struct {
	f *os.File
}

func acquireLock(path string) (*fileLock, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}

	var overlapped syscall.Overlapped
	r1, _, err := syscall.SyscallN(
		procLockFileEx.Addr(),
		uintptr(f.Fd()),
		lockfileExclusiveLock,
		0,
		1,
		0,
		uintptr(unsafe.Pointer(&overlapped)),
	)
	if r1 == 0 {
		f.Close()
		return nil, err
	}

	return &fileLock{f: f}, nil
}

func (l *fileLock) Release() error {
	if l.f == nil {
		return nil
	}
	r1, _, _ := syscall.SyscallN(
		procUnlockFile.Addr(),
		uintptr(l.f.Fd()),
		1,
		0,
		1,
		0,
	)
	closeErr := l.f.Close()
	l.f = nil
	if r1 == 0 {
		return closeErr
	}
	return closeErr
}
