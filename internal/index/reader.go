package index

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"slices"
	"sort"
	"sync"

	"codeg/internal/model"
)

// Reader gives access to one encoded package file. Open verifies the header
// and checksum; sections are decoded on first use and cached, so a query that
// needs only the symbol index never decodes the occurrences. A Reader is safe
// for concurrent use.
type Reader struct {
	data     []byte
	sections [numSections + 1][]byte

	strs    func() ([]string, error)
	keys    func() ([]model.Key, error)
	meta    func() (*meta, error)
	files   func() ([]model.FileFact, error)
	symbols func() ([]model.Symbol, error)
	occs    func() ([]model.Occurrence, error)
	symIdx  func() (*symIndex, error)
	calls   func() ([]CallEdge, error)
	edges   func() ([]model.Edge, error)
	msets   func() ([]model.MethodSet, error)
	exts    func() ([]model.External, error)
}

type meta struct {
	path, name  string
	hasErrors   bool
	imports     []string
	diagnostics []model.Diagnostic
}

// CallEdge is one distinct (caller, callee, dispatch) with its call-site count.
// A zero Callee is a call whose target is unknown.
type CallEdge struct {
	Caller, Callee model.Key
	Dispatch       model.Dispatch
	Sites          int
}

type symIndex struct {
	keys   []uint32 // sorted key-table indexes of symbols that occur
	starts []uint32 // ids[starts[i]:starts[i+1]] belong to keys[i]
	ids    []uint32
}

// Open validates data and returns a Reader over it. The Reader keeps data;
// the caller must not modify it.
func Open(data []byte) (*Reader, error) {
	if len(data) < headerSize+trailerSize || string(data[:4]) != magic {
		return nil, ErrMagic
	}
	if v := binary.LittleEndian.Uint16(data[4:]); v != Version {
		return nil, fmt.Errorf("%w: file has version %d, reader supports %d", ErrVersion, v, Version)
	}
	if flags := binary.LittleEndian.Uint16(data[6:]); flags != 0 {
		return nil, fmt.Errorf("%w: unknown flags %#x", ErrCorrupt, flags)
	}
	body := data[:len(data)-trailerSize]
	if binary.LittleEndian.Uint32(data[len(body):]) != crc32.Checksum(body, castagnoli) {
		return nil, ErrChecksum
	}

	r := &Reader{data: data}
	d := &decoder{b: body, off: headerSize}
	for id := secStrings; int(id) <= numSections; id++ {
		if got := sectionID(d.byte()); d.err == nil && got != id {
			return nil, fmt.Errorf("%w: section %d where %d expected", ErrCorrupt, got, id)
		}
		n := d.uvarint()
		if d.err != nil || n > uint64(len(body)-d.off) {
			return nil, fmt.Errorf("%w: truncated section %d", ErrCorrupt, id)
		}
		r.sections[id] = d.bytes(int(n))
	}
	if d.off != len(body) {
		return nil, fmt.Errorf("%w: trailing bytes after sections", ErrCorrupt)
	}

	r.strs = sync.OnceValues(r.decodeStrings)
	r.keys = sync.OnceValues(r.decodeKeys)
	r.meta = sync.OnceValues(r.decodeMeta)
	r.files = sync.OnceValues(r.decodeFiles)
	r.symbols = sync.OnceValues(r.decodeSymbols)
	r.occs = sync.OnceValues(r.decodeOccurrences)
	r.symIdx = sync.OnceValues(r.decodeSymbolIndex)
	r.calls = sync.OnceValues(r.decodeCalls)
	r.edges = sync.OnceValues(r.decodeEdges)
	r.msets = sync.OnceValues(r.decodeMethodSets)
	r.exts = sync.OnceValues(r.decodeExternals)
	return r, nil
}

// Decode reads a whole package file into PackageFacts.
func Decode(data []byte) (*model.PackageFacts, error) {
	r, err := Open(data)
	if err != nil {
		return nil, err
	}
	return r.Facts()
}

// Facts decodes everything.
func (r *Reader) Facts() (*model.PackageFacts, error) {
	m, err := r.meta()
	if err != nil {
		return nil, err
	}
	f := &model.PackageFacts{
		Path: m.path, Name: m.name, HasErrors: m.hasErrors,
		Imports: m.imports, Diagnostics: m.diagnostics,
	}
	if f.Files, err = r.files(); err != nil {
		return nil, err
	}
	if f.Symbols, err = r.symbols(); err != nil {
		return nil, err
	}
	if f.Occurrences, err = r.occs(); err != nil {
		return nil, err
	}
	if f.Edges, err = r.edges(); err != nil {
		return nil, err
	}
	if f.MethodSets, err = r.msets(); err != nil {
		return nil, err
	}
	if f.Externals, err = r.exts(); err != nil {
		return nil, err
	}
	// The lookup sections are derived data, but they are part of the file, so
	// a full decode still validates them.
	if _, err = r.symIdx(); err != nil {
		return nil, err
	}
	if _, err = r.calls(); err != nil {
		return nil, err
	}
	return f, nil
}

// Path is the import path of the package.
func (r *Reader) Path() (string, error) {
	m, err := r.meta()
	if err != nil {
		return "", err
	}
	return m.path, nil
}

// Imports are the import paths of the package's direct imports.
func (r *Reader) Imports() ([]string, error) {
	m, err := r.meta()
	if err != nil {
		return nil, err
	}
	return m.imports, nil
}

// Files returns the package's files, indexed by Range.File.
func (r *Reader) Files() ([]model.FileFact, error) { return r.files() }

// Symbols returns all symbols, sorted by key.
func (r *Reader) Symbols() ([]model.Symbol, error) { return r.symbols() }

// Symbol looks up one symbol by key.
func (r *Reader) Symbol(key model.Key) (model.Symbol, bool, error) {
	syms, err := r.symbols()
	if err != nil {
		return model.Symbol{}, false, err
	}
	i, ok := slices.BinarySearchFunc(syms, key, func(s model.Symbol, k model.Key) int { return s.Key.Compare(k) })
	if !ok {
		return model.Symbol{}, false, nil
	}
	return syms[i], true, nil
}

// Occurrences returns all occurrences in position order.
func (r *Reader) Occurrences() ([]model.Occurrence, error) { return r.occs() }

// OccurrencesOf returns the occurrences of a symbol, in position order, using
// the by-symbol index instead of scanning.
func (r *Reader) OccurrencesOf(key model.Key) ([]model.Occurrence, error) {
	ki, err := r.keyIndex(key)
	if err != nil || ki < 0 {
		return nil, err
	}
	ix, err := r.symIdx()
	if err != nil {
		return nil, err
	}
	g := sort.Search(len(ix.keys), func(i int) bool { return ix.keys[i] >= uint32(ki) })
	if g == len(ix.keys) || ix.keys[g] != uint32(ki) {
		return nil, nil
	}
	occs, err := r.occs()
	if err != nil {
		return nil, err
	}
	ids := ix.ids[ix.starts[g]:ix.starts[g+1]]
	out := make([]model.Occurrence, len(ids))
	for i, id := range ids {
		out[i] = occs[id]
	}
	return out, nil
}

// Callees returns the call edges out of caller.
func (r *Reader) Callees(caller model.Key) ([]CallEdge, error) {
	edges, err := r.calls()
	if err != nil {
		return nil, err
	}
	lo := sort.Search(len(edges), func(i int) bool { return edges[i].Caller.Compare(caller) >= 0 })
	hi := lo
	for hi < len(edges) && edges[hi].Caller == caller {
		hi++
	}
	return edges[lo:hi], nil
}

// Callers returns the call edges into callee, in caller order.
func (r *Reader) Callers(callee model.Key) ([]CallEdge, error) {
	edges, err := r.calls()
	if err != nil {
		return nil, err
	}
	var out []CallEdge
	for _, e := range edges {
		if e.Callee == callee {
			out = append(out, e)
		}
	}
	return out, nil
}

// Edges returns the structural edges.
func (r *Reader) Edges() ([]model.Edge, error) { return r.edges() }

// MethodSets returns the flattened method sets, sorted by type key.
func (r *Reader) MethodSets() ([]model.MethodSet, error) { return r.msets() }

// Externals returns the reference-only stubs.
func (r *Reader) Externals() ([]model.External, error) { return r.exts() }

// keyIndex finds key in the sorted key table, or -1.
func (r *Reader) keyIndex(key model.Key) (int, error) {
	keys, err := r.keys()
	if err != nil {
		return -1, err
	}
	i, ok := slices.BinarySearchFunc(keys, key, model.Key.Compare)
	if !ok {
		return -1, nil
	}
	return i, nil
}

func (r *Reader) section(id sectionID) *decoder { return &decoder{b: r.sections[id]} }

func (r *Reader) str(d *decoder, strs []string) string {
	i := d.u32()
	if d.err == nil && int(i) >= len(strs) {
		d.fail("string index out of range")
		return ""
	}
	if d.err != nil {
		return ""
	}
	return strs[i]
}

func (r *Reader) key(d *decoder, keys []model.Key) model.Key {
	return r.keyAt(d, keys, d.u32())
}

func (r *Reader) keyAt(d *decoder, keys []model.Key, i uint32) model.Key {
	if d.err == nil && int(i) >= len(keys) {
		d.fail("key index out of range")
		return model.Key{}
	}
	if d.err != nil {
		return model.Key{}
	}
	return keys[i]
}

func (r *Reader) decodeStrings() ([]string, error) {
	d := r.section(secStrings)
	n := d.count(1)
	strs := make([]string, n)
	// One allocation for all string bytes; each string is a substring of it.
	all := string(r.sections[secStrings])
	for i := range strs {
		l := int(d.u32())
		off := d.off
		d.bytes(l)
		if d.err == nil {
			strs[i] = all[off : off+l]
		}
	}
	if err := d.done(); err != nil {
		return nil, err
	}
	if n == 0 || strs[0] != "" {
		return nil, fmt.Errorf("%w: string table must start with the empty string", ErrCorrupt)
	}
	return strs, nil
}

func (r *Reader) decodeKeys() ([]model.Key, error) {
	strs, err := r.strs()
	if err != nil {
		return nil, err
	}
	d := r.section(secKeys)
	n := d.count(2)
	keys := make([]model.Key, n)
	for i := range keys {
		keys[i] = model.Key{Pkg: r.str(d, strs), Name: r.str(d, strs)}
	}
	if err := d.done(); err != nil {
		return nil, err
	}
	if n == 0 || keys[0] != (model.Key{}) {
		return nil, fmt.Errorf("%w: key table must start with the zero key", ErrCorrupt)
	}
	return keys, nil
}

func (r *Reader) decodeMeta() (*meta, error) {
	strs, err := r.strs()
	if err != nil {
		return nil, err
	}
	d := r.section(secMeta)
	m := &meta{path: r.str(d, strs), name: r.str(d, strs), hasErrors: d.byte() != 0}
	if n := d.count(1); n > 0 {
		m.imports = make([]string, n)
		for i := range m.imports {
			m.imports[i] = r.str(d, strs)
		}
	}
	if n := d.count(4); n > 0 {
		m.diagnostics = make([]model.Diagnostic, n)
		for i := range m.diagnostics {
			m.diagnostics[i] = model.Diagnostic{
				File: r.str(d, strs), Line: int(d.u32()), Col: int(d.u32()), Msg: r.str(d, strs),
			}
		}
	}
	return m, d.done()
}

func (r *Reader) decodeFiles() ([]model.FileFact, error) {
	strs, err := r.strs()
	if err != nil {
		return nil, err
	}
	d := r.section(secFiles)
	n := d.count(2)
	var files []model.FileFact
	if n > 0 {
		files = make([]model.FileFact, n)
	}
	for i := range files {
		files[i].Path = r.str(d, strs)
		fl := d.byte()
		files[i].Generated, files[i].Test = fl&1 != 0, fl&2 != 0
	}
	return files, d.done()
}

func readRange(d *decoder) model.Range {
	switch tag := d.byte(); tag {
	case 0:
		return model.Range{}
	case 2:
		return model.Range{File: int32(d.u32())}
	case 1:
		var r model.Range
		r.File = int32(d.u32())
		r.Start = int32(d.u32())
		r.End = r.Start + int32(d.u32())
		r.Line = int32(d.u32())
		r.Col = int32(d.u32())
		r.EndLine = r.Line + int32(d.u32())
		r.EndCol = int32(d.u32())
		if r.End < r.Start || r.EndLine < r.Line {
			d.fail("range overflow")
		}
		return r
	default:
		d.fail("bad range tag")
		return model.Range{}
	}
}

func (r *Reader) decodeSymbols() ([]model.Symbol, error) {
	strs, err := r.strs()
	if err != nil {
		return nil, err
	}
	keys, err := r.keys()
	if err != nil {
		return nil, err
	}
	d := r.section(secSymbols)
	n := d.count(6)
	var syms []model.Symbol
	if n > 0 {
		syms = make([]model.Symbol, n)
	}
	var prev int64
	for i := range syms {
		prev += d.svarint()
		if prev < 0 || prev > int64(^uint32(0)>>1) {
			d.fail("key index out of range")
			break
		}
		s := &syms[i]
		s.Key = r.keyAt(d, keys, uint32(prev))
		kb := d.byte()
		s.Kind, s.Exported = model.Kind(kb&0x7f), kb&0x80 != 0
		s.Name = r.str(d, strs)
		s.Detail = r.str(d, strs)
		s.Pos = readRange(d)
		s.Decl = readRange(d)
	}
	return syms, d.done()
}

// column reads n varints as int64 with fn deciding how each is interpreted.
func (r *Reader) decodeOccurrences() ([]model.Occurrence, error) {
	strs, err := r.strs()
	if err != nil {
		return nil, err
	}
	keys, err := r.keys()
	if err != nil {
		return nil, err
	}
	d := r.section(secOccurrences)
	n := d.count(16)
	if n == 0 {
		return nil, d.done()
	}
	occs := make([]model.Occurrence, n)

	var prevFile int64
	for i := range occs {
		prevFile += d.svarint()
		occs[i].File = int32(prevFile)
	}
	var base int64
	prevFile = -1
	for i := range occs {
		if int64(occs[i].File) != prevFile {
			base = 0
		}
		base += d.svarint()
		occs[i].Start, prevFile = int32(base), int64(occs[i].File)
	}
	prevFile, base = -1, 0
	for i := range occs {
		if int64(occs[i].File) != prevFile {
			base = 0
		}
		base += d.svarint()
		occs[i].Line, prevFile = int32(base), int64(occs[i].File)
	}
	for i := range occs {
		occs[i].End = occs[i].Start + int32(d.u32())
	}
	for i := range occs {
		occs[i].Col = int32(d.u32())
	}
	for i := range occs {
		occs[i].EndLine = occs[i].Line + int32(d.u32())
	}
	for i := range occs {
		occs[i].EndCol = int32(d.u32())
	}
	for i := range occs {
		occs[i].Symbol = r.key(d, keys)
	}
	for i := range occs {
		b := d.byte()
		occs[i].Role, occs[i].Dispatch = model.Role(b&0xf), model.Dispatch(b>>4)
	}
	for i := range occs {
		occs[i].Flags = model.Flags(d.u32())
	}
	var encl int64
	for i := range occs {
		encl += d.svarint()
		if encl < 0 || encl > int64(^uint32(0)>>1) {
			d.fail("key index out of range")
			break
		}
		occs[i].Enclosing = r.keyAt(d, keys, uint32(encl))
	}
	for i := range occs {
		occs[i].Call = int32(d.u32())
	}
	for i := range occs {
		occs[i].ArgOf = int32(d.u32())
	}
	for i := range occs {
		occs[i].ArgIndex = int32(d.u32())
	}
	for i := range occs {
		occs[i].FlowTo = r.key(d, keys)
	}
	for i := range occs {
		occs[i].TypeArgs = r.str(d, strs)
	}
	if err := d.done(); err != nil {
		return nil, err
	}
	return occs, nil
}

func (r *Reader) decodeSymbolIndex() (*symIndex, error) {
	keys, err := r.keys()
	if err != nil {
		return nil, err
	}
	occs, err := r.occs()
	if err != nil {
		return nil, err
	}
	d := r.section(secSymbolIndex)
	g := d.count(2)
	ix := &symIndex{keys: make([]uint32, 0, g), starts: make([]uint32, 0, g+1)}
	var key uint64
	for range g {
		key += d.uvarint()
		if d.err == nil && key >= uint64(len(keys)) {
			d.fail("key index out of range")
			break
		}
		cnt := d.count(1)
		ix.keys = append(ix.keys, uint32(key))
		ix.starts = append(ix.starts, uint32(len(ix.ids)))
		var id uint64
		for range cnt {
			id += d.uvarint()
			if d.err == nil && id >= uint64(len(occs)) {
				d.fail("occurrence id out of range")
				break
			}
			ix.ids = append(ix.ids, uint32(id))
		}
	}
	ix.starts = append(ix.starts, uint32(len(ix.ids)))
	if err := d.done(); err != nil {
		return nil, err
	}
	if len(ix.ids) != len(occs) {
		return nil, fmt.Errorf("%w: symbol index covers %d of %d occurrences", ErrCorrupt, len(ix.ids), len(occs))
	}
	return ix, nil
}

func (r *Reader) decodeCalls() ([]CallEdge, error) {
	keys, err := r.keys()
	if err != nil {
		return nil, err
	}
	d := r.section(secCalls)
	n := d.count(4)
	var edges []CallEdge
	if n > 0 {
		edges = make([]CallEdge, n)
	}
	var caller, callee int64
	for i := range edges {
		delta := d.uvarint()
		if delta != 0 {
			callee = 0
		}
		caller += int64(delta)
		callee += d.svarint()
		if caller < 0 || callee < 0 || caller > int64(^uint32(0)>>1) || callee > int64(^uint32(0)>>1) {
			d.fail("key index out of range")
			break
		}
		edges[i].Caller = r.keyAt(d, keys, uint32(caller))
		edges[i].Callee = r.keyAt(d, keys, uint32(callee))
		edges[i].Dispatch = model.Dispatch(d.byte())
		edges[i].Sites = int(d.u32())
	}
	return edges, d.done()
}

func (r *Reader) decodeEdges() ([]model.Edge, error) {
	keys, err := r.keys()
	if err != nil {
		return nil, err
	}
	d := r.section(secEdges)
	n := d.count(3)
	var edges []model.Edge
	if n > 0 {
		edges = make([]model.Edge, n)
	}
	for i := range edges {
		edges[i] = model.Edge{Kind: model.EdgeKind(d.byte()), From: r.key(d, keys), To: r.key(d, keys)}
	}
	return edges, d.done()
}

func (r *Reader) decodeMethodSets() ([]model.MethodSet, error) {
	strs, err := r.strs()
	if err != nil {
		return nil, err
	}
	keys, err := r.keys()
	if err != nil {
		return nil, err
	}
	d := r.section(secMethodSets)
	n := d.count(3)
	var sets []model.MethodSet
	if n > 0 {
		sets = make([]model.MethodSet, n)
	}
	for i := range sets {
		sets[i].Type = r.key(d, keys)
		sets[i].Interface = d.byte() != 0
		if m := d.count(3); m > 0 {
			sets[i].Methods = make([]model.MethodEntry, m)
			for j := range sets[i].Methods {
				e := model.MethodEntry{Name: r.str(d, strs), Sig: r.str(d, strs)}
				fl := d.byte()
				e.PtrRecv, e.Promoted = fl&1 != 0, fl&2 != 0
				sets[i].Methods[j] = e
			}
		}
	}
	return sets, d.done()
}

func (r *Reader) decodeExternals() ([]model.External, error) {
	strs, err := r.strs()
	if err != nil {
		return nil, err
	}
	keys, err := r.keys()
	if err != nil {
		return nil, err
	}
	d := r.section(secExternals)
	n := d.count(6)
	var exts []model.External
	if n > 0 {
		exts = make([]model.External, n)
	}
	for i := range exts {
		exts[i] = model.External{
			Key: r.key(d, keys), Kind: model.Kind(d.byte()),
			Name: r.str(d, strs), Detail: r.str(d, strs), Module: r.str(d, strs), Version: r.str(d, strs),
		}
	}
	return exts, d.done()
}
