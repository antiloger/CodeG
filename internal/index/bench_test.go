package index_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/klauspost/compress/zstd"
	"google.golang.org/protobuf/proto"

	"codeg/internal/index"
	"codeg/internal/index/internal/pbbench"
	"codeg/internal/model"
)

// toProto converts facts to the baseline protobuf schema, with the same
// tables-and-indexes design as the hand-written format.
func toProto(f *model.PackageFacts) *pbbench.Facts {
	strIdx := map[string]uint32{"": 0}
	strs := []string{""}
	s := func(v string) uint32 {
		if i, ok := strIdx[v]; ok {
			return i
		}
		i := uint32(len(strs))
		strIdx[v] = i
		strs = append(strs, v)
		return i
	}
	keyIdx := map[model.Key]uint32{{}: 0}
	keys := []*pbbench.Key{{}}
	k := func(v model.Key) uint32 {
		if i, ok := keyIdx[v]; ok {
			return i
		}
		i := uint32(len(keys))
		keyIdx[v] = i
		keys = append(keys, &pbbench.Key{Pkg: s(v.Pkg), Name: s(v.Name)})
		return i
	}
	rng := func(r model.Range) *pbbench.Range {
		if r == (model.Range{}) {
			return nil
		}
		return &pbbench.Range{File: r.File, Start: r.Start, End: r.End, Line: r.Line, Col: r.Col, EndLine: r.EndLine, EndCol: r.EndCol}
	}

	out := &pbbench.Facts{Path: s(f.Path), Name: s(f.Name), HasErrors: f.HasErrors}
	for _, ff := range f.Files {
		out.Files = append(out.Files, &pbbench.File{Path: s(ff.Path), Generated: ff.Generated, Test: ff.Test})
	}
	for _, imp := range f.Imports {
		out.Imports = append(out.Imports, s(imp))
	}
	for i := range f.Symbols {
		sy := &f.Symbols[i]
		out.Symbols = append(out.Symbols, &pbbench.Symbol{
			Key: k(sy.Key), Kind: uint32(sy.Kind), Exported: sy.Exported,
			Name: s(sy.Name), Detail: s(sy.Detail), Pos: rng(sy.Pos), Decl: rng(sy.Decl),
		})
	}
	out.Occurrences = make([]*pbbench.Occurrence, 0, len(f.Occurrences))
	for i := range f.Occurrences {
		o := &f.Occurrences[i]
		out.Occurrences = append(out.Occurrences, &pbbench.Occurrence{
			Range: rng(o.Range), Symbol: k(o.Symbol), Role: uint32(o.Role), Dispatch: uint32(o.Dispatch),
			Flags: uint32(o.Flags), Enclosing: k(o.Enclosing), Call: o.Call, ArgOf: o.ArgOf,
			ArgIndex: o.ArgIndex, FlowTo: k(o.FlowTo), TypeArgs: s(o.TypeArgs),
		})
	}
	for _, e := range f.Edges {
		out.Edges = append(out.Edges, &pbbench.Edge{Kind: uint32(e.Kind), From: k(e.From), To: k(e.To)})
	}
	for _, ms := range f.MethodSets {
		m := &pbbench.MethodSet{Type: k(ms.Type), Interface: ms.Interface}
		for _, e := range ms.Methods {
			m.Methods = append(m.Methods, &pbbench.MethodEntry{Name: s(e.Name), Sig: s(e.Sig), PtrRecv: e.PtrRecv})
		}
		out.MethodSets = append(out.MethodSets, m)
	}
	for _, x := range f.Externals {
		out.Externals = append(out.Externals, &pbbench.External{
			Key: k(x.Key), Kind: uint32(x.Kind), Name: s(x.Name), Detail: s(x.Detail), Module: s(x.Module), Version: s(x.Version),
		})
	}
	out.Strings, out.Keys = strs, keys
	return out
}

// biggest returns the facts with the most occurrences: the worst case for a
// single package file.
func biggest(tb testing.TB) *model.PackageFacts {
	facts := mustRealFacts(tb)
	return facts[slices.IndexFunc(facts, func(f *model.PackageFacts) bool {
		for _, g := range facts {
			if len(g.Occurrences) > len(f.Occurrences) {
				return false
			}
		}
		return true
	})]
}

func TestFormatSize(t *testing.T) {
	zenc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		t.Fatal(err)
	}
	var totOurs, totPB, totOursZ, totPBZ, totOccs int
	t.Logf("%-28s %8s %9s %9s %9s %9s %7s", "package", "occs", "ours", "protobuf", "ours+zstd", "pb+zstd", "B/occ")
	for _, f := range mustRealFacts(t) {
		ours, err := index.Encode(f)
		if err != nil {
			t.Fatal(err)
		}
		pb, err := proto.Marshal(toProto(f))
		if err != nil {
			t.Fatal(err)
		}
		oz, pz := len(zenc.EncodeAll(ours, nil)), len(zenc.EncodeAll(pb, nil))
		perOcc := 0.0
		if len(f.Occurrences) > 0 {
			perOcc = float64(len(ours)) / float64(len(f.Occurrences))
		}
		if len(f.Occurrences) > 2000 {
			t.Logf("%-28s %8d %9d %9d %9d %9d %7.1f", f.Path, len(f.Occurrences), len(ours), len(pb), oz, pz, perOcc)
		}
		totOurs, totPB, totOursZ, totPBZ, totOccs = totOurs+len(ours), totPB+len(pb), totOursZ+oz, totPBZ+pz, totOccs+len(f.Occurrences)
	}
	t.Logf("%-28s %8d %9d %9d %9d %9d", "TOTAL", totOccs, totOurs, totPB, totOursZ, totPBZ)
	t.Logf("ours is %.0f%% of protobuf raw, %.0f%% with zstd on both; zstd shrinks ours by %.0f%%",
		100*float64(totOurs)/float64(totPB), 100*float64(totOursZ)/float64(totPBZ), 100-100*float64(totOursZ)/float64(totOurs))
}

func BenchmarkEncode(b *testing.B) {
	f := biggest(b)
	pbf := toProto(f)
	b.Run("ours", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			data, err := index.Encode(f)
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(data)))
		}
	})
	// Like for like: ours builds its string and key tables inside Encode.
	b.Run("protobuf-with-tables", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			data, err := proto.Marshal(toProto(f))
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(data)))
		}
	})
	b.Run("protobuf-marshal-only", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			data, err := proto.Marshal(pbf)
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(data)))
		}
	})
}

func BenchmarkDecode(b *testing.B) {
	f := biggest(b)
	ours, _ := index.Encode(f)
	pb, _ := proto.Marshal(toProto(f))
	b.Logf("%s: %d occurrences, ours %d B, protobuf %d B", f.Path, len(f.Occurrences), len(ours), len(pb))
	b.Run("ours-full", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(ours)))
		for i := 0; i < b.N; i++ {
			if _, err := index.Decode(ours); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("protobuf-full", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(pb)))
		for i := 0; i < b.N; i++ {
			var m pbbench.Facts
			if err := proto.Unmarshal(pb, &m); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkSymbolQuery is the operation the server does most: open a package
// and read the occurrences of one symbol.
func BenchmarkSymbolQuery(b *testing.B) {
	f := biggest(b)
	ours, _ := index.Encode(f)
	pb, _ := proto.Marshal(toProto(f))
	// A symbol with several occurrences.
	counts := map[model.Key]int{}
	var target model.Key
	for _, o := range f.Occurrences {
		if counts[o.Symbol]++; counts[o.Symbol] > counts[target] && o.Symbol.Pkg != "" {
			target = o.Symbol
		}
	}
	b.Run("ours-open+index", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			r, err := index.Open(ours)
			if err != nil {
				b.Fatal(err)
			}
			if got, err := r.OccurrencesOf(target); err != nil || len(got) != counts[target] {
				b.Fatalf("got %d, %v want %d", len(got), err, counts[target])
			}
		}
	})
	b.Run("protobuf-unmarshal+scan", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var m pbbench.Facts
			if err := proto.Unmarshal(pb, &m); err != nil {
				b.Fatal(err)
			}
			n := 0
			for j, k := range m.Keys {
				if m.Strings[k.Pkg] == target.Pkg && m.Strings[k.Name] == target.Name {
					for _, o := range m.Occurrences {
						if o.Symbol == uint32(j) {
							n++
						}
					}
					break
				}
			}
			if n != counts[target] {
				b.Fatalf("got %d want %d", n, counts[target])
			}
		}
	})
}

var _ = fmt.Sprint
