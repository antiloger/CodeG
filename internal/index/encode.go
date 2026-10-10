package index

import (
	"cmp"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"slices"

	"codeg/internal/model"
)

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// Section layouts (S = string-table index, K = key-table index, all varints):
//
//	strings   n | n * (len, bytes)                    sorted; index 0 is ""
//	keys      n | n * (S pkg, S name)                 sorted; index 0 is the zero key
//	meta      S path, S name, hasErrors u8, imports (n, S...), diagnostics (n, S file, line, col, S msg)
//	files     n | n * (S path, flags u8)
//	symbols   n | n * (K delta, kind|exported<<7 u8, S name, S detail, range pos, range decl)
//	          range: tag u8 (0 zero, 1 full, 2 file only) then, for full,
//	          file, start, end-start, line, col, endLine-line, endCol
//	occurrences n | one column per field, n values each:
//	          file (zigzag delta), start, line (zigzag delta within a file), length,
//	          col, endLine-line, endCol, K symbol, role|dispatch<<4 u8, flags,
//	          K enclosing (zigzag delta), call, argOf, argIndex, K flowTo, S typeArgs
//	symindex  g | g * (K delta, count, occurrence ids ascending, first absolute then deltas)
//	calls     n | n * (K caller delta, callee (delta within a caller), dispatch u8, count)
//	edges     n | n * (kind u8, K from, K to)
//	methodsets n | n * (K type, interface u8, m, m * (S name, S sig, flags u8: 1 ptr-only, 2 promoted))
//	externals n | n * (K, kind u8, S name, S detail, S module, S version)

// Encode serializes f. The output is a pure function of f: equal facts give
// identical bytes.
func Encode(f *model.PackageFacts) ([]byte, error) {
	if err := validate(f); err != nil {
		return nil, err
	}
	e := &encoder{f: f}
	e.collect()

	sections := [numSections + 1][]byte{
		secStrings:     e.strings(),
		secKeys:        e.keys(),
		secMeta:        e.meta(),
		secFiles:       e.files(),
		secSymbols:     e.symbols(),
		secOccurrences: e.occurrences(),
		secSymbolIndex: e.symbolIndex(),
		secCalls:       e.calls(),
		secEdges:       e.edges(),
		secMethodSets:  e.methodSets(),
		secExternals:   e.externals(),
	}
	size := headerSize + trailerSize
	for _, s := range sections {
		size += len(s) + 6
	}
	out := make([]byte, 0, size)
	out = append(out, magic...)
	out = binary.LittleEndian.AppendUint16(out, Version)
	out = binary.LittleEndian.AppendUint16(out, 0) // flags: reserved for compression
	for id := secStrings; int(id) <= numSections; id++ {
		out = append(out, byte(id))
		out = binary.AppendUvarint(out, uint64(len(sections[id])))
		out = append(out, sections[id]...)
	}
	out = binary.LittleEndian.AppendUint32(out, crc32.Checksum(out, castagnoli))
	return out, nil
}

// validate rejects facts the format cannot represent: negative or inverted
// ranges. The extractor never produces them.
func validate(f *model.PackageFacts) error {
	check := func(what string, r model.Range) error {
		if r.File < 0 || r.Start < 0 || r.End < r.Start || r.Line < 0 || r.Col < 0 || r.EndLine < r.Line || r.EndCol < 0 {
			return fmt.Errorf("index: invalid range %+v in %s", r, what)
		}
		return nil
	}
	for _, d := range f.Diagnostics {
		if d.Line < 0 || d.Col < 0 {
			return fmt.Errorf("index: negative position in diagnostic %q", d.Msg)
		}
	}
	for i := range f.Symbols {
		if err := check("symbol "+f.Symbols[i].Key.String(), f.Symbols[i].Pos); err != nil {
			return err
		}
		if err := check("symbol "+f.Symbols[i].Key.String(), f.Symbols[i].Decl); err != nil {
			return err
		}
	}
	for i := range f.Occurrences {
		o := &f.Occurrences[i]
		if err := check("occurrence", o.Range); err != nil {
			return err
		}
		if o.Call < 0 || o.ArgOf < 0 || o.ArgIndex < 0 {
			return fmt.Errorf("index: negative call fields in occurrence at %d:%d", o.Line, o.Col)
		}
		if o.Role > 15 || o.Dispatch > 15 {
			return fmt.Errorf("index: role or dispatch out of range at %d:%d", o.Line, o.Col)
		}
	}
	for i := range f.Symbols {
		if f.Symbols[i].Kind > 127 {
			return fmt.Errorf("index: kind out of range for %s", f.Symbols[i].Key)
		}
	}
	return nil
}

type encoder struct {
	f *model.PackageFacts

	strs   []string
	strIdx map[string]uint32
	keys_  []model.Key
	keyIdx map[model.Key]uint32

	// Key-table indexes of each occurrence's symbol, enclosing symbol and
	// flow target, resolved once: map lookups on keys are the encoder's main cost.
	occSym, occEncl, occFlow []uint32
}

func (e *encoder) s(str string) uint64    { return uint64(e.strIdx[str]) }
func (e *encoder) k(key model.Key) uint64 { return uint64(e.keyIdx[key]) }

// collect builds the string and key tables: sorted, deduplicated, with the
// empty string and the zero key at index 0.
func (e *encoder) collect() {
	f := e.f
	keySet := map[model.Key]struct{}{{}: {}}
	addKey := func(k model.Key) { keySet[k] = struct{}{} }
	for i := range f.Symbols {
		addKey(f.Symbols[i].Key)
	}
	var prevEncl, prevFlow model.Key // consecutive occurrences usually share these
	for i := range f.Occurrences {
		o := &f.Occurrences[i]
		addKey(o.Symbol)
		if o.Enclosing != prevEncl {
			addKey(o.Enclosing)
			prevEncl = o.Enclosing
		}
		if o.FlowTo != prevFlow {
			addKey(o.FlowTo)
			prevFlow = o.FlowTo
		}
	}
	for _, ed := range f.Edges {
		addKey(ed.From)
		addKey(ed.To)
	}
	for _, ms := range f.MethodSets {
		addKey(ms.Type)
	}
	for _, x := range f.Externals {
		addKey(x.Key)
	}
	e.keys_ = make([]model.Key, 0, len(keySet))
	for k := range keySet {
		e.keys_ = append(e.keys_, k)
	}
	slices.SortFunc(e.keys_, model.Key.Compare)
	e.keyIdx = make(map[model.Key]uint32, len(e.keys_))
	for i, k := range e.keys_ {
		e.keyIdx[k] = uint32(i)
	}

	strSet := map[string]struct{}{"": {}}
	add := func(s string) { strSet[s] = struct{}{} }
	for _, k := range e.keys_ {
		add(k.Pkg)
		add(k.Name)
	}
	add(f.Path)
	add(f.Name)
	for _, s := range f.Imports {
		add(s)
	}
	for _, d := range f.Diagnostics {
		add(d.File)
		add(d.Msg)
	}
	for _, ff := range f.Files {
		add(ff.Path)
	}
	for i := range f.Symbols {
		add(f.Symbols[i].Name)
		add(f.Symbols[i].Detail)
	}
	for i := range f.Occurrences {
		add(f.Occurrences[i].TypeArgs)
	}
	for _, ms := range f.MethodSets {
		for _, m := range ms.Methods {
			add(m.Name)
			add(m.Sig)
		}
	}
	for _, x := range f.Externals {
		add(x.Name)
		add(x.Detail)
		add(x.Module)
		add(x.Version)
	}
	n := len(f.Occurrences)
	e.occSym, e.occEncl, e.occFlow = make([]uint32, n), make([]uint32, n), make([]uint32, n)
	var lastEncl, lastFlow model.Key
	var lastEnclIdx, lastFlowIdx uint32
	for i := range f.Occurrences {
		o := &f.Occurrences[i]
		e.occSym[i] = e.keyIdx[o.Symbol]
		if o.Enclosing != lastEncl || i == 0 {
			lastEncl, lastEnclIdx = o.Enclosing, e.keyIdx[o.Enclosing]
		}
		if o.FlowTo != lastFlow || i == 0 {
			lastFlow, lastFlowIdx = o.FlowTo, e.keyIdx[o.FlowTo]
		}
		e.occEncl[i], e.occFlow[i] = lastEnclIdx, lastFlowIdx
	}

	e.strs = make([]string, 0, len(strSet))
	for s := range strSet {
		e.strs = append(e.strs, s)
	}
	slices.Sort(e.strs)
	e.strIdx = make(map[string]uint32, len(e.strs))
	for i, s := range e.strs {
		e.strIdx[s] = uint32(i)
	}
}

func (e *encoder) strings() []byte {
	var b []byte
	b = binary.AppendUvarint(b, uint64(len(e.strs)))
	for _, s := range e.strs {
		b = binary.AppendUvarint(b, uint64(len(s)))
		b = append(b, s...)
	}
	return b
}

func (e *encoder) keys() []byte {
	var b []byte
	b = binary.AppendUvarint(b, uint64(len(e.keys_)))
	for _, k := range e.keys_ {
		b = binary.AppendUvarint(b, e.s(k.Pkg))
		b = binary.AppendUvarint(b, e.s(k.Name))
	}
	return b
}

func boolByte(v bool) byte {
	if v {
		return 1
	}
	return 0
}

func (e *encoder) meta() []byte {
	f := e.f
	var b []byte
	b = binary.AppendUvarint(b, e.s(f.Path))
	b = binary.AppendUvarint(b, e.s(f.Name))
	b = append(b, boolByte(f.HasErrors))
	b = binary.AppendUvarint(b, uint64(len(f.Imports)))
	for _, imp := range f.Imports {
		b = binary.AppendUvarint(b, e.s(imp))
	}
	b = binary.AppendUvarint(b, uint64(len(f.Diagnostics)))
	for _, d := range f.Diagnostics {
		b = binary.AppendUvarint(b, e.s(d.File))
		b = binary.AppendUvarint(b, uint64(d.Line))
		b = binary.AppendUvarint(b, uint64(d.Col))
		b = binary.AppendUvarint(b, e.s(d.Msg))
	}
	return b
}

func (e *encoder) files() []byte {
	var b []byte
	b = binary.AppendUvarint(b, uint64(len(e.f.Files)))
	for _, ff := range e.f.Files {
		b = binary.AppendUvarint(b, e.s(ff.Path))
		b = append(b, boolByte(ff.Generated)|boolByte(ff.Test)<<1)
	}
	return b
}

func appendRange(b []byte, r model.Range) []byte {
	switch {
	case r == (model.Range{}):
		return append(b, 0)
	case r == (model.Range{File: r.File}):
		b = append(b, 2)
		return binary.AppendUvarint(b, uint64(r.File))
	}
	b = append(b, 1)
	b = binary.AppendUvarint(b, uint64(r.File))
	b = binary.AppendUvarint(b, uint64(r.Start))
	b = binary.AppendUvarint(b, uint64(r.End-r.Start))
	b = binary.AppendUvarint(b, uint64(r.Line))
	b = binary.AppendUvarint(b, uint64(r.Col))
	b = binary.AppendUvarint(b, uint64(r.EndLine-r.Line))
	return binary.AppendUvarint(b, uint64(r.EndCol))
}

func (e *encoder) symbols() []byte {
	var b []byte
	b = binary.AppendUvarint(b, uint64(len(e.f.Symbols)))
	var prev uint64
	for i := range e.f.Symbols {
		s := &e.f.Symbols[i]
		k := e.k(s.Key)
		b = binary.AppendUvarint(b, zigzag(int64(k)-int64(prev)))
		prev = k
		b = append(b, byte(s.Kind)|boolByte(s.Exported)<<7)
		b = binary.AppendUvarint(b, e.s(s.Name))
		b = binary.AppendUvarint(b, e.s(s.Detail))
		b = appendRange(b, s.Pos)
		b = appendRange(b, s.Decl)
	}
	return b
}

func (e *encoder) occurrences() []byte {
	occs := e.f.Occurrences
	n := len(occs)
	b := make([]byte, 0, n*20)
	b = binary.AppendUvarint(b, uint64(n))

	// Position columns delta against the previous occurrence of the same
	// file; a file change restarts from zero.
	var prevFile, prevStart, prevLine int64
	for i := range occs {
		b = binary.AppendUvarint(b, zigzag(int64(occs[i].File)-prevFile))
		prevFile = int64(occs[i].File)
	}
	prevFile = -1
	for i := range occs {
		o := &occs[i]
		if int64(o.File) != prevFile {
			prevStart = 0
		}
		b = binary.AppendUvarint(b, zigzag(int64(o.Start)-prevStart))
		prevStart, prevFile = int64(o.Start), int64(o.File)
	}
	prevFile = -1
	for i := range occs {
		o := &occs[i]
		if int64(o.File) != prevFile {
			prevLine = 0
		}
		b = binary.AppendUvarint(b, zigzag(int64(o.Line)-prevLine))
		prevLine, prevFile = int64(o.Line), int64(o.File)
	}
	for i := range occs {
		b = binary.AppendUvarint(b, uint64(occs[i].End-occs[i].Start))
	}
	for i := range occs {
		b = binary.AppendUvarint(b, uint64(occs[i].Col))
	}
	for i := range occs {
		b = binary.AppendUvarint(b, uint64(occs[i].EndLine-occs[i].Line))
	}
	for i := range occs {
		b = binary.AppendUvarint(b, uint64(occs[i].EndCol))
	}
	for i := range occs {
		b = binary.AppendUvarint(b, uint64(e.occSym[i]))
	}
	for i := range occs {
		b = append(b, byte(occs[i].Role)|byte(occs[i].Dispatch)<<4)
	}
	for i := range occs {
		b = binary.AppendUvarint(b, uint64(occs[i].Flags))
	}
	var prevEncl int64
	for i := range occs {
		k := int64(e.occEncl[i])
		b = binary.AppendUvarint(b, zigzag(k-prevEncl))
		prevEncl = k
	}
	for i := range occs {
		b = binary.AppendUvarint(b, uint64(occs[i].Call))
	}
	for i := range occs {
		b = binary.AppendUvarint(b, uint64(occs[i].ArgOf))
	}
	for i := range occs {
		b = binary.AppendUvarint(b, uint64(occs[i].ArgIndex))
	}
	for i := range occs {
		b = binary.AppendUvarint(b, uint64(e.occFlow[i]))
	}
	for i := range occs {
		b = binary.AppendUvarint(b, e.s(occs[i].TypeArgs))
	}
	return b
}

// symbolIndex maps each symbol to the ids (positions in the occurrence
// array) where it occurs, so "references to X" reads only X's occurrences.
func (e *encoder) symbolIndex() []byte {
	// Counting sort of occurrence ids by symbol key index: O(n + keys), and
	// stable, so ids stay ascending within a symbol.
	start := make([]uint32, len(e.keys_)+1)
	for _, k := range e.occSym {
		start[k+1]++
	}
	for i := 1; i < len(start); i++ {
		start[i] += start[i-1]
	}
	ids := make([]uint32, len(e.occSym))
	next := slices.Clone(start[:len(start)-1])
	for id, k := range e.occSym {
		ids[next[k]] = uint32(id)
		next[k]++
	}

	groups := 0
	for k := range e.keys_ {
		if start[k+1] > start[k] {
			groups++
		}
	}
	b := make([]byte, 0, len(ids)*2+groups*3+8)
	b = binary.AppendUvarint(b, uint64(groups))
	var prevKey uint32
	for k := range e.keys_ {
		lo, hi := start[k], start[k+1]
		if hi == lo {
			continue
		}
		b = binary.AppendUvarint(b, uint64(uint32(k)-prevKey))
		prevKey = uint32(k)
		b = binary.AppendUvarint(b, uint64(hi-lo))
		var prev uint32
		for _, id := range ids[lo:hi] {
			b = binary.AppendUvarint(b, uint64(id-prev))
			prev = id
		}
	}
	return b
}

type callPair struct {
	caller, callee uint32
	dispatch       model.Dispatch
}

// calls lists each distinct (caller, callee, dispatch) with its number of call
// sites, sorted, for traversal without touching occurrences.
func (e *encoder) calls() []byte {
	counts := map[callPair]uint32{}
	for i := range e.f.Occurrences {
		o := &e.f.Occurrences[i]
		if o.Role != model.RoleCall {
			continue
		}
		counts[callPair{e.occEncl[i], e.occSym[i], o.Dispatch}]++
	}
	pairs := make([]callPair, 0, len(counts))
	for p := range counts {
		pairs = append(pairs, p)
	}
	slices.SortFunc(pairs, func(a, b callPair) int {
		return cmp.Or(cmp.Compare(a.caller, b.caller), cmp.Compare(a.callee, b.callee), cmp.Compare(a.dispatch, b.dispatch))
	})
	var b []byte
	b = binary.AppendUvarint(b, uint64(len(pairs)))
	var prevCaller, prevCallee uint32
	for _, p := range pairs {
		b = binary.AppendUvarint(b, uint64(p.caller-prevCaller))
		if p.caller != prevCaller {
			prevCallee = 0
		}
		b = binary.AppendUvarint(b, zigzag(int64(p.callee)-int64(prevCallee)))
		b = append(b, byte(p.dispatch))
		b = binary.AppendUvarint(b, uint64(counts[p]))
		prevCaller, prevCallee = p.caller, p.callee
	}
	return b
}

func (e *encoder) edges() []byte {
	var b []byte
	b = binary.AppendUvarint(b, uint64(len(e.f.Edges)))
	for _, ed := range e.f.Edges {
		b = append(b, byte(ed.Kind))
		b = binary.AppendUvarint(b, e.k(ed.From))
		b = binary.AppendUvarint(b, e.k(ed.To))
	}
	return b
}

func (e *encoder) methodSets() []byte {
	var b []byte
	b = binary.AppendUvarint(b, uint64(len(e.f.MethodSets)))
	for _, ms := range e.f.MethodSets {
		b = binary.AppendUvarint(b, e.k(ms.Type))
		b = append(b, boolByte(ms.Interface))
		b = binary.AppendUvarint(b, uint64(len(ms.Methods)))
		for _, m := range ms.Methods {
			b = binary.AppendUvarint(b, e.s(m.Name))
			b = binary.AppendUvarint(b, e.s(m.Sig))
			b = append(b, boolByte(m.PtrRecv)|boolByte(m.Promoted)<<1)
		}
	}
	return b
}

func (e *encoder) externals() []byte {
	var b []byte
	b = binary.AppendUvarint(b, uint64(len(e.f.Externals)))
	for _, x := range e.f.Externals {
		b = binary.AppendUvarint(b, e.k(x.Key))
		b = append(b, byte(x.Kind))
		b = binary.AppendUvarint(b, e.s(x.Name))
		b = binary.AppendUvarint(b, e.s(x.Detail))
		b = binary.AppendUvarint(b, e.s(x.Module))
		b = binary.AppendUvarint(b, e.s(x.Version))
	}
	return b
}
