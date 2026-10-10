//go:build !unix

package index

import (
	"fmt"
	"os"
	"path/filepath"
)

// Lock takes the store's exclusive lock by creating the lock file exclusively.
// Unlike the unix version a crash leaves the file behind; delete it by hand.
func (s *Store) Lock() (unlock func(), err error) {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(s.Dir, "lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("%w (%s)", ErrLocked, path)
		}
		return nil, err
	}
	f.Close()
	return func() { os.Remove(path) }, nil
}
