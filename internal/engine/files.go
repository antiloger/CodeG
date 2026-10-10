package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"time"

	"codeg/internal/index"
)

// fileHasher hashes source files, skipping the read when a file's size and
// modification time match the manifest. Stat data is only trusted if it is
// clearly older than the manifest that recorded it: a file edited in the same
// clock tick as the index run could keep the same size and mtime (the "racy
// git" problem), so recent files are always re-hashed.
type fileHasher struct {
	root  string
	cache map[string]index.FileEntry // by root-relative path
	trust int64                      // stat data with mtime before this is trusted
}

const racyWindow = 2 * time.Second

func newFileHasher(root string, old *index.Manifest) *fileHasher {
	h := &fileHasher{root: root, cache: map[string]index.FileEntry{}}
	if old == nil {
		return h
	}
	h.trust = old.WrittenAt - int64(racyWindow)
	for i := range old.Packages {
		for _, f := range old.Packages[i].Files {
			h.cache[f.Path] = f
		}
	}
	return h
}

func (h *fileHasher) entry(abs string, kind index.FileKind) (index.FileEntry, error) {
	rel := relPath(h.root, abs)
	st, err := os.Stat(abs)
	if err != nil {
		return index.FileEntry{}, err
	}
	e := index.FileEntry{Path: rel, Kind: kind, Size: st.Size(), ModTime: st.ModTime().UnixNano()}
	if c, ok := h.cache[rel]; ok && c.Size == e.Size && c.ModTime == e.ModTime && e.ModTime < h.trust && c.Hash != "" {
		e.Hash = c.Hash
		return e, nil
	}
	f, err := os.Open(filepath.Clean(abs))
	if err != nil {
		return index.FileEntry{}, err
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return index.FileEntry{}, err
	}
	e.Hash = hex.EncodeToString(sum.Sum(nil))
	return e, nil
}
