package index

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

const (
	manifestMagic   = "CGMF"
	ManifestVersion = 1
)

// BuildConfig is the one build configuration an index is made for.
type BuildConfig struct {
	GoVersion string
	GOOS      string
	GOARCH    string
	CGO       string // "0" or "1"
	Tags      []string
}

// FileKind classifies a file that a package's input hash covers.
type FileKind uint8

const (
	FileGo       FileKind = iota // compiled Go source
	FileOther                    // non-Go source (C, assembly) and embedded files
	FileExcluded                 // Go file excluded by build constraints
)

// FileEntry is a file that is part of a package's input, with the stat data
// used to skip re-hashing it.
type FileEntry struct {
	Path    string // relative to the module root when inside it
	Kind    FileKind
	Size    int64
	ModTime int64 // unix nanoseconds
	Hash    string
	Reason  string // for excluded files: why they are not indexed
}

// PackageEntry describes one indexed package.
type PackageEntry struct {
	Path      string // import path; external test packages end in _test
	ID        string // go/packages ID of the indexed variant
	Name      string
	Dir       string
	Module    string // module path
	Version   string
	InputHash string
	APIHash   string
	File      string // package file name inside pkg/
	HasErrors bool
	Imports   []string
	Files     []FileEntry
}

// Manifest is the index's table of contents. Replacing manifest.bin is the
// commit point: a reader sees either the old index or the new one.
type Manifest struct {
	Schema    uint32 // extractor schema version; a mismatch invalidates every package
	Config    BuildConfig
	EnvHash   string // go.mod, go.sum, go.work and vendor state
	Root      string
	WrittenAt int64 // unix nanoseconds; stat data newer than this is not trusted
	Summary   string
	Packages  []PackageEntry // sorted by Path
}

// Package returns the entry for an import path.
func (m *Manifest) Package(path string) *PackageEntry {
	lo, hi := 0, len(m.Packages)
	for lo < hi {
		mid := (lo + hi) / 2
		if m.Packages[mid].Path < path {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(m.Packages) && m.Packages[lo].Path == path {
		return &m.Packages[lo]
	}
	return nil
}

func (m *Manifest) Encode() []byte {
	w := &buf{}
	w.b = append(w.b, manifestMagic...)
	w.b = binary.LittleEndian.AppendUint16(w.b, ManifestVersion)
	w.u(uint64(m.Schema))
	w.s(m.Config.GoVersion)
	w.s(m.Config.GOOS)
	w.s(m.Config.GOARCH)
	w.s(m.Config.CGO)
	w.strs(m.Config.Tags)
	w.s(m.EnvHash)
	w.s(m.Root)
	w.u(uint64(m.WrittenAt))
	w.s(m.Summary)
	w.u(uint64(len(m.Packages)))
	for i := range m.Packages {
		p := &m.Packages[i]
		w.s(p.Path)
		w.s(p.ID)
		w.s(p.Name)
		w.s(p.Dir)
		w.s(p.Module)
		w.s(p.Version)
		w.s(p.InputHash)
		w.s(p.APIHash)
		w.s(p.File)
		w.bool(p.HasErrors)
		w.strs(p.Imports)
		w.u(uint64(len(p.Files)))
		for _, f := range p.Files {
			w.s(f.Path)
			w.byte(byte(f.Kind))
			w.u(uint64(f.Size))
			w.u(uint64(f.ModTime))
			w.s(f.Hash)
			w.s(f.Reason)
		}
	}
	w.b = binary.LittleEndian.AppendUint32(w.b, crc32.Checksum(w.b, castagnoli))
	return w.b
}

// DecodeManifest parses and verifies a manifest file.
func DecodeManifest(data []byte) (*Manifest, error) {
	if len(data) < 6+trailerSize || string(data[:4]) != manifestMagic {
		return nil, ErrMagic
	}
	if v := binary.LittleEndian.Uint16(data[4:]); v != ManifestVersion {
		return nil, fmt.Errorf("%w: manifest version %d", ErrVersion, v)
	}
	body := data[:len(data)-trailerSize]
	if binary.LittleEndian.Uint32(data[len(body):]) != crc32.Checksum(body, castagnoli) {
		return nil, ErrChecksum
	}
	d := &decoder{b: body, off: 6}
	m := &Manifest{Schema: d.u32()}
	m.Config = BuildConfig{GoVersion: d.str(), GOOS: d.str(), GOARCH: d.str(), CGO: d.str(), Tags: d.strs()}
	m.EnvHash, m.Root = d.str(), d.str()
	m.WrittenAt = int64(d.uvarint())
	m.Summary = d.str()
	if n := d.count(10); n > 0 {
		m.Packages = make([]PackageEntry, n)
		for i := range m.Packages {
			p := &m.Packages[i]
			p.Path, p.ID, p.Name, p.Dir, p.Module, p.Version = d.str(), d.str(), d.str(), d.str(), d.str(), d.str()
			p.InputHash, p.APIHash, p.File = d.str(), d.str(), d.str()
			p.HasErrors = d.byte() != 0
			p.Imports = d.strs()
			if nf := d.count(6); nf > 0 {
				p.Files = make([]FileEntry, nf)
				for j := range p.Files {
					f := &p.Files[j]
					f.Path, f.Kind = d.str(), FileKind(d.byte())
					f.Size, f.ModTime = int64(d.uvarint()), int64(d.uvarint())
					f.Hash, f.Reason = d.str(), d.str()
				}
			}
		}
	}
	if err := d.done(); err != nil {
		return nil, err
	}
	return m, nil
}

var ErrNoIndex = errors.New("index: no index in this directory")
