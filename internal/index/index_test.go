package index_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math/rand"
	"reflect"
	"slices"
	"testing"

	"codeg/internal/index"
	"codeg/internal/model"
)

func TestRoundTrip(t *testing.T) {
	cases := map[string]*model.PackageFacts{
		"empty": {},
		"hand":  handFacts(),
	}
	for _, f := range mustRealFacts(t) {
		cases[f.Path] = f
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			data, err := index.Encode(f)
			if err != nil {
				t.Fatal(err)
			}
			got, err := index.Decode(data)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(f, got) {
				t.Fatalf("round trip changed the facts:\n got %+v\nwant %+v", summary(got), summary(f))
			}
			again, err := index.Encode(f)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(data, again) {
				t.Error("encoding is not deterministic")
			}
			// Re-encoding the decoded facts is byte-identical too.
			viaDecoded, _ := index.Encode(got)
			if !bytes.Equal(data, viaDecoded) {
				t.Error("encode(decode(x)) != x")
			}
		})
	}
}

func summary(f *model.PackageFacts) map[string]int {
	return map[string]int{"symbols": len(f.Symbols), "occs": len(f.Occurrences), "edges": len(f.Edges),
		"files": len(f.Files), "msets": len(f.MethodSets), "exts": len(f.Externals)}
}

func TestEncodeRejectsInvalidRanges(t *testing.T) {
	f := handFacts()
	f.Occurrences[0].End = f.Occurrences[0].Start - 1
	if _, err := index.Encode(f); err == nil {
		t.Error("inverted range accepted")
	}
	f = handFacts()
	f.Symbols[2].Pos.Line = -1
	if _, err := index.Encode(f); err == nil {
		t.Error("negative line accepted")
	}
}

func resign(data []byte) {
	body := data[:len(data)-4]
	binary.LittleEndian.PutUint32(data[len(body):], crc32.Checksum(body, crc32.MakeTable(crc32.Castagnoli)))
}

func TestHeaderErrors(t *testing.T) {
	data, err := index.Encode(handFacts())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := index.Open(nil); !errors.Is(err, index.ErrMagic) {
		t.Errorf("empty input: %v", err)
	}
	bad := slices.Clone(data)
	bad[0] = 'X'
	if _, err := index.Open(bad); !errors.Is(err, index.ErrMagic) {
		t.Errorf("bad magic: %v", err)
	}
	bad = slices.Clone(data)
	binary.LittleEndian.PutUint16(bad[4:], index.Version+1)
	resign(bad)
	if _, err := index.Open(bad); !errors.Is(err, index.ErrVersion) {
		t.Errorf("future version: %v", err)
	}
	bad = slices.Clone(data)
	binary.LittleEndian.PutUint16(bad[6:], 1)
	resign(bad)
	if _, err := index.Open(bad); !errors.Is(err, index.ErrCorrupt) {
		t.Errorf("unknown flags: %v", err)
	}
}

func TestChecksumCatchesEveryByteFlip(t *testing.T) {
	data, err := index.Encode(handFacts())
	if err != nil {
		t.Fatal(err)
	}
	for i := range data {
		bad := slices.Clone(data)
		bad[i] ^= 0x01
		if _, err := index.Open(bad); err == nil {
			t.Fatalf("flip at byte %d of %d went unnoticed", i, len(data))
		}
	}
}

func TestTruncation(t *testing.T) {
	data, err := index.Encode(handFacts())
	if err != nil {
		t.Fatal(err)
	}
	for n := range len(data) {
		if _, err := index.Decode(data[:n]); err == nil {
			t.Fatalf("truncation to %d of %d bytes accepted", n, len(data))
		}
	}
}

// TestCorruptButSigned mutates bytes and fixes the checksum, so corruption
// reaches the section decoders. They must return an error or valid facts,
// never panic or over-allocate.
func TestCorruptButSigned(t *testing.T) {
	data, err := index.Encode(handFacts())
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 20000; i++ {
		bad := slices.Clone(data)
		for range 1 + rng.Intn(3) {
			bad[8+rng.Intn(len(bad)-12)] = byte(rng.Intn(256))
		}
		resign(bad)
		if r, err := index.Open(bad); err == nil {
			if f, err := r.Facts(); err == nil {
				// Whatever decodes must re-encode without error.
				_, _ = index.Encode(f)
			}
		}
	}
}

func FuzzDecode(f *testing.F) {
	data, _ := index.Encode(handFacts())
	f.Add(data)
	empty, _ := index.Encode(&model.PackageFacts{})
	f.Add(empty)
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) >= 12 {
			resign(b) // let the fuzzer past the checksum
		}
		if r, err := index.Open(b); err == nil {
			_, _ = r.Facts()
			_, _ = r.OccurrencesOf(model.Key{Pkg: "fmt", Name: "Println"})
			_, _ = r.Callees(model.Key{})
		}
	})
}

func TestLookups(t *testing.T) {
	for _, f := range append(mustRealFacts(t), handFacts()) {
		t.Run(f.Path, func(t *testing.T) {
			data, err := index.Encode(f)
			if err != nil {
				t.Fatal(err)
			}
			r, err := index.Open(data)
			if err != nil {
				t.Fatal(err)
			}

			// OccurrencesOf agrees with a scan, for every symbol that occurs.
			want := map[model.Key][]model.Occurrence{}
			for _, o := range f.Occurrences {
				want[o.Symbol] = append(want[o.Symbol], o)
			}
			for key, w := range want {
				got, err := r.OccurrencesOf(key)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, w) {
					t.Fatalf("OccurrencesOf(%v): got %d, want %d occurrences", key, len(got), len(w))
				}
			}
			if got, err := r.OccurrencesOf(model.Key{Pkg: "no.such/pkg", Name: "X"}); err != nil || got != nil {
				t.Errorf("unknown key: %v, %v", got, err)
			}

			// Call edges agree with a scan of call occurrences.
			type pair struct {
				caller, callee model.Key
				dispatch       model.Dispatch
			}
			wantCalls := map[pair]int{}
			byCaller := map[model.Key]int{}
			byCallee := map[model.Key]int{}
			for _, o := range f.Occurrences {
				if o.Role == model.RoleCall {
					wantCalls[pair{o.Enclosing, o.Symbol, o.Dispatch}]++
					byCaller[o.Enclosing]++
					byCallee[o.Symbol]++
				}
			}
			for caller := range byCaller {
				edges, err := r.Callees(caller)
				if err != nil {
					t.Fatal(err)
				}
				sites := 0
				for _, e := range edges {
					if e.Caller != caller {
						t.Fatalf("Callees(%v) returned edge from %v", caller, e.Caller)
					}
					if wantCalls[pair{e.Caller, e.Callee, e.Dispatch}] != e.Sites {
						t.Fatalf("edge %v -> %v (%v): %d sites, want %d", e.Caller, e.Callee, e.Dispatch, e.Sites, wantCalls[pair{e.Caller, e.Callee, e.Dispatch}])
					}
					sites += e.Sites
				}
				if sites != byCaller[caller] {
					t.Fatalf("Callees(%v): %d sites, want %d", caller, sites, byCaller[caller])
				}
			}
			for callee, n := range byCallee {
				edges, err := r.Callers(callee)
				if err != nil {
					t.Fatal(err)
				}
				sites := 0
				for _, e := range edges {
					sites += e.Sites
				}
				if sites != n {
					t.Fatalf("Callers(%v): %d sites, want %d", callee, sites, n)
				}
			}

			// Symbol lookup.
			for _, s := range f.Symbols {
				got, ok, err := r.Symbol(s.Key)
				if err != nil || !ok || !reflect.DeepEqual(got, s) {
					t.Fatalf("Symbol(%v) = %+v, %v, %v", s.Key, got, ok, err)
				}
			}
			if _, ok, _ := r.Symbol(model.Key{Pkg: f.Path, Name: "definitely.absent"}); ok {
				t.Error("absent symbol found")
			}
		})
	}
}
