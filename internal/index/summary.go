package index

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"sort"
)

const (
	summaryMagic   = "CGSM"
	SummaryVersion = 3
)

// SummaryPackage is the per-package data that lets queries decide which
// package files to open without opening any.
type SummaryPackage struct {
	Path    string
	Imports []string // direct imports
	// Refs are the packages that own a symbol this package refers to. It is
	// wider than Imports: an embedded method promoted from a package that is
	// not imported still counts.
	Refs []string
	// Methods are the names of all methods in the package's method sets
	// (concrete types and interfaces), qualified with the package path when
	// unexported. "implements" uses them to skip packages that cannot match.
	Methods []string
}

// SummaryDep names the module that provides a package outside the project
// (one that some project package imports).
type SummaryDep struct {
	Path    string // package import path
	Module  string // module path; empty for the standard library
	Version string
}

// Summary is the whole-index summary. It is small and always loaded, unlike
// package files.
type Summary struct {
	Packages []SummaryPackage // sorted by Path
	Deps     []SummaryDep     // sorted by Path
}

// Dep returns the module of an external package.
func (s *Summary) Dep(path string) (SummaryDep, bool) {
	i := sort.Search(len(s.Deps), func(i int) bool { return s.Deps[i].Path >= path })
	if i < len(s.Deps) && s.Deps[i].Path == path {
		return s.Deps[i], true
	}
	return SummaryDep{}, false
}

func (s *Summary) Package(path string) *SummaryPackage {
	i := sort.Search(len(s.Packages), func(i int) bool { return s.Packages[i].Path >= path })
	if i < len(s.Packages) && s.Packages[i].Path == path {
		return &s.Packages[i]
	}
	return nil
}

func (s *Summary) Encode() []byte {
	w := &buf{}
	w.b = append(w.b, summaryMagic...)
	w.b = binary.LittleEndian.AppendUint16(w.b, SummaryVersion)
	w.u(uint64(len(s.Packages)))
	for _, p := range s.Packages {
		w.s(p.Path)
		w.strs(p.Imports)
		w.strs(p.Refs)
		w.strs(p.Methods)
	}
	w.u(uint64(len(s.Deps)))
	for _, d := range s.Deps {
		w.s(d.Path)
		w.s(d.Module)
		w.s(d.Version)
	}
	w.b = binary.LittleEndian.AppendUint32(w.b, crc32.Checksum(w.b, castagnoli))
	return w.b
}

func DecodeSummary(data []byte) (*Summary, error) {
	if len(data) < 6+trailerSize || string(data[:4]) != summaryMagic {
		return nil, ErrMagic
	}
	if v := binary.LittleEndian.Uint16(data[4:]); v != SummaryVersion {
		return nil, fmt.Errorf("%w: summary version %d", ErrVersion, v)
	}
	body := data[:len(data)-trailerSize]
	if binary.LittleEndian.Uint32(data[len(body):]) != crc32.Checksum(body, castagnoli) {
		return nil, ErrChecksum
	}
	d := &decoder{b: body, off: 6}
	s := &Summary{}
	if n := d.count(4); n > 0 {
		s.Packages = make([]SummaryPackage, n)
		for i := range s.Packages {
			s.Packages[i] = SummaryPackage{Path: d.str(), Imports: d.strs(), Refs: d.strs(), Methods: d.strs()}
		}
	}
	if n := d.count(3); n > 0 {
		s.Deps = make([]SummaryDep, n)
		for i := range s.Deps {
			s.Deps[i] = SummaryDep{Path: d.str(), Module: d.str(), Version: d.str()}
		}
	}
	if err := d.done(); err != nil {
		return nil, err
	}
	return s, nil
}
