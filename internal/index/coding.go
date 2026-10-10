// Package index defines the on-disk format of one package's facts and the
// reader and writer for it.
//
// A package file is:
//
//	header    "CGPK" | version u16 LE | flags u16 LE
//	sections  id u8 | length uvarint | body   (repeated, in ascending id order)
//	trailer   CRC-32C (Castagnoli) of everything before it, u32 LE
//
// All integers inside sections are varints. Strings and keys are stored once,
// in shared tables, and referenced by index everywhere else. See encode.go
// for the section layouts.
package index

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

const (
	magic = "CGPK"

	// Version is the format version this package reads and writes. Any change
	// to a section layout must bump it: readers reject other versions, and the
	// index engine treats a version mismatch as "re-index this package".
	Version = 2

	headerSize  = 8
	trailerSize = 4
)

var (
	ErrMagic    = errors.New("index: not a codeg package file")
	ErrVersion  = errors.New("index: unsupported format version")
	ErrChecksum = errors.New("index: checksum mismatch")
	ErrCorrupt  = errors.New("index: corrupt package file")
)

type sectionID uint8

const (
	secStrings sectionID = iota + 1
	secKeys
	secMeta
	secFiles
	secSymbols
	secOccurrences
	secSymbolIndex
	secCalls
	secEdges
	secMethodSets
	secExternals
	numSections = int(secExternals)
)

func zigzag(v int64) uint64   { return uint64(v<<1) ^ uint64(v>>63) }
func unzigzag(u uint64) int64 { return int64(u>>1) ^ -int64(u&1) }

// decoder reads varints from a byte slice. Errors are sticky: after the first
// failure every read returns zero, so callers check err once at the end.
type decoder struct {
	b   []byte
	off int
	err error
}

func (d *decoder) fail(what string) {
	if d.err == nil {
		d.err = fmt.Errorf("%w: %s at offset %d", ErrCorrupt, what, d.off)
	}
}

func (d *decoder) uvarint() uint64 {
	if d.err != nil {
		return 0
	}
	v, n := binary.Uvarint(d.b[d.off:])
	if n <= 0 {
		d.fail("bad varint")
		return 0
	}
	d.off += n
	return v
}

func (d *decoder) svarint() int64 { return unzigzag(d.uvarint()) }

// u32 reads a uvarint that must fit in 31 bits, the range of every count,
// index and coordinate in the format.
func (d *decoder) u32() uint32 {
	v := d.uvarint()
	if v > math.MaxInt32 {
		d.fail("value out of range")
		return 0
	}
	return uint32(v)
}

func (d *decoder) byte() byte {
	if d.err != nil {
		return 0
	}
	if d.off >= len(d.b) {
		d.fail("unexpected end")
		return 0
	}
	c := d.b[d.off]
	d.off++
	return c
}

func (d *decoder) bytes(n int) []byte {
	if d.err != nil {
		return nil
	}
	if n < 0 || n > len(d.b)-d.off {
		d.fail("unexpected end")
		return nil
	}
	s := d.b[d.off : d.off+n]
	d.off += n
	return s
}

// count reads an element count and checks that minBytes*count bytes remain,
// so a corrupt count cannot trigger a huge allocation.
func (d *decoder) count(minBytes int) int {
	n := d.u32()
	if d.err == nil && int(n)*minBytes > len(d.b)-d.off {
		d.fail("count exceeds data")
		return 0
	}
	return int(n)
}

func (d *decoder) done() error {
	if d.err == nil && d.off != len(d.b) {
		d.fail("trailing bytes in section")
	}
	return d.err
}

func (d *decoder) str() string {
	n := d.u32()
	if d.err != nil {
		return ""
	}
	return string(d.bytes(int(n)))
}

// encoder helpers shared by the manifest and summary files.

type buf struct{ b []byte }

func (w *buf) u(v uint64)  { w.b = binary.AppendUvarint(w.b, v) }
func (w *buf) s(v string)  { w.u(uint64(len(v))); w.b = append(w.b, v...) }
func (w *buf) byte(v byte) { w.b = append(w.b, v) }
func (w *buf) bool(v bool) { w.byte(boolByte(v)) }
func (w *buf) strs(v []string) {
	w.u(uint64(len(v)))
	for _, s := range v {
		w.s(s)
	}
}

func (d *decoder) strs() []string {
	n := d.count(1)
	if n == 0 {
		return nil
	}
	out := make([]string, n)
	for i := range out {
		out[i] = d.str()
	}
	return out
}
