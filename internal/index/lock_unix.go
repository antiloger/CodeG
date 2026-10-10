//go:build unix

package index

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Lock takes the store's exclusive lock, or fails with ErrLocked if another
// process holds it. The lock is released when the process exits, so a crashed
// indexer never leaves a stale lock.
func (s *Store) Lock() (unlock func(), err error) {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, "lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, fmt.Errorf("%w (%s)", ErrLocked, filepath.Join(s.Dir, "lock"))
		}
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
