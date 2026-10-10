package index

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Store is a .codeg directory:
//
//	manifest.bin              table of contents; replacing it commits a new index
//	summary.<hash>.bin        whole-index summary, referenced by the manifest
//	pkg/<id>.<inputhash>.bin  everything owned by one package
//	lock                      held by a running indexer
//
// Package and summary files are never modified once written, and their names
// include their content hash, so a reader holding the old manifest keeps
// reading consistent files while an indexer prepares the next generation.
type Store struct{ Dir string }

func OpenStore(dir string) *Store { return &Store{Dir: dir} }

const manifestName = "manifest.bin"

// PackageFileName is the file name for a package's facts at an input hash.
func PackageFileName(pkgPath, inputHash string) string {
	id := sha256.Sum256([]byte(pkgPath))
	h := inputHash
	if len(h) > 16 {
		h = h[:16]
	}
	return hex.EncodeToString(id[:6]) + "." + h + ".bin"
}

// SummaryFileName is the file name for a summary with the given contents.
func SummaryFileName(data []byte) string {
	sum := sha256.Sum256(data)
	return "summary." + hex.EncodeToString(sum[:8]) + ".bin"
}

func (s *Store) path(parts ...string) string {
	return filepath.Join(append([]string{s.Dir}, parts...)...)
}

// ReadManifest loads the committed manifest, or returns ErrNoIndex.
func (s *Store) ReadManifest() (*Manifest, error) {
	data, err := os.ReadFile(s.path(manifestName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoIndex
	}
	if err != nil {
		return nil, err
	}
	return DecodeManifest(data)
}

// ReadSummary loads the summary the manifest refers to.
func (s *Store) ReadSummary(m *Manifest) (*Summary, error) {
	data, err := os.ReadFile(s.path(m.Summary))
	if err != nil {
		return nil, err
	}
	return DecodeSummary(data)
}

// OpenPackage opens the facts file of a package entry.
func (s *Store) OpenPackage(e *PackageEntry) (*Reader, error) {
	data, err := os.ReadFile(s.path("pkg", e.File))
	if err != nil {
		return nil, err
	}
	return Open(data)
}

// WritePackage stores a package file. The name includes the input hash, so an
// existing file of that name already holds these facts and is simply replaced.
func (s *Store) WritePackage(name string, data []byte) error {
	return s.writeFile(s.path("pkg", name), data)
}

func (s *Store) WriteSummary(name string, data []byte) error {
	return s.writeFile(s.path(name), data)
}

// Commit makes m the current index by atomically replacing the manifest. All
// files it refers to must already be written.
func (s *Store) Commit(m *Manifest) error {
	return s.writeFile(s.path(manifestName), m.Encode())
}

// writeFile writes data to a temporary file, syncs it, and renames it into
// place, so a crash never leaves a partial file under the final name.
func (s *Store) writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// GC removes package files, summaries and leftover temporaries that the
// committed manifest does not refer to. Call it only after Commit.
//
// Files that any of prev refers to are kept too. A reader that opened the
// previous generation before the commit loads package files lazily; deleting
// them the moment the manifest is replaced would fail its in-flight requests.
// They are removed by the next run's GC instead.
func (s *Store) GC(m *Manifest, prev ...*Manifest) (removed []string, err error) {
	keep := map[string]bool{manifestName: true, "lock": true}
	for _, gen := range append([]*Manifest{m}, prev...) {
		if gen == nil {
			continue
		}
		keep[gen.Summary] = true
		for i := range gen.Packages {
			keep[filepath.Join("pkg", gen.Packages[i].File)] = true
		}
	}
	root := s.Dir
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if keep[rel] {
			return nil
		}
		if strings.HasPrefix(rel, "pkg"+string(filepath.Separator)) || strings.HasPrefix(rel, "summary.") || strings.HasPrefix(filepath.Base(rel), ".tmp-") {
			if rmErr := os.Remove(p); rmErr != nil {
				return rmErr
			}
			removed = append(removed, rel)
		}
		return nil
	})
	return removed, err
}

var ErrLocked = errors.New("index: another indexer holds the lock")
