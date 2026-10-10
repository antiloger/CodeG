// Package model defines symbol identity for the code graph.
//
// Keys never include a module version and never use byte offsets. Every key
// names its owning package explicitly, so a symbol's owner is a field rather
// than something parsed out of a string (import paths contain dots).
package model

import (
	"cmp"
	"strings"
)

// Key identifies a symbol. Pkg is the import path of the owning package, or
// empty for universe symbols (int, error, len, ...). Name is the symbol's name
// within the package; see namer.go for the grammar.
type Key struct {
	Pkg  string
	Name string
}

// String renders the key as pkgpath.Name. It is for display and debugging;
// use the fields to find the owner.
func (k Key) String() string {
	switch {
	case k.Name == "":
		return k.Pkg
	case k.Pkg == "":
		return k.Name
	}
	return k.Pkg + "." + k.Name
}

// Compare orders keys by package, then name.
func (k Key) Compare(o Key) int {
	if c := strings.Compare(k.Pkg, o.Pkg); c != 0 {
		return c
	}
	return cmp.Compare(k.Name, o.Name)
}

// IsUniverse reports whether the key names a predeclared symbol.
func (k Key) IsUniverse() bool { return k.Pkg == "" && k.Name != "" }

// Ref is a key together with the kind of symbol it names.
type Ref struct {
	Key  Key
	Kind Kind
}
