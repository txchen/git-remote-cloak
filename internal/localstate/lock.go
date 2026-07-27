package localstate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// OperationLock prevents two mutating Cloak operations from constructing a
// publication from the same Logical Repository at the same time. The kernel
// releases the advisory lock if the process exits unexpectedly.
type OperationLock struct {
	file *os.File
}

// AcquireOperationLock obtains the Logical Repository's non-blocking local
// operation lock.
func AcquireOperationLock(gitDirectory string) (*OperationLock, error) {
	if gitDirectory == "" {
		return &OperationLock{}, nil
	}
	directory := filepath.Join(gitDirectory, "cloak")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create local Cloak state directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(directory, "operation.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open local operation lock: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, errors.New("another Cloak publication or maintenance operation is already using this Logical Repository")
		}
		return nil, fmt.Errorf("acquire local operation lock: %w", err)
	}
	return &OperationLock{file: file}, nil
}

// Close releases the local operation lock.
func (lock *OperationLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	unlockErr := unix.Flock(int(lock.file.Fd()), unix.LOCK_UN)
	closeErr := lock.file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
