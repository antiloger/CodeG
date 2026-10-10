package query

import "codeg/internal/model"

// SymbolRef describes a symbol in a result.
type SymbolRef struct {
	Key      string    `json:"key"`
	Package  string    `json:"package"`
	Name     string    `json:"name"`
	Kind     string    `json:"kind"`
	Detail   string    `json:"detail,omitempty"`
	Location *Location `json:"location,omitempty"` // where it is defined; absent for external symbols
	External bool      `json:"external,omitempty"` // owned by a dependency: a reference-only stub
	Module   string    `json:"module,omitempty"`
	Version  string    `json:"version,omitempty"`

	k model.Key
}

// Key returns the symbol's key.
func (s SymbolRef) ModelKey() model.Key { return s.k }

// Occ is one occurrence of a symbol.
type Occ struct {
	Location
	Symbol    string   `json:"symbol,omitempty"` // empty for a call whose target is unknown
	Role      string   `json:"role"`
	Dispatch  string   `json:"dispatch,omitempty"`
	Flags     []string `json:"flags,omitempty"`
	Enclosing string   `json:"enclosing,omitempty"`
	Call      int      `json:"call,omitempty"`
	ArgOf     int      `json:"argOf,omitempty"`
	ArgIndex  int      `json:"argIndex,omitempty"`
	FlowTo    string   `json:"flowTo,omitempty"`
	TypeArgs  string   `json:"typeArgs,omitempty"`
	// Text is the source line, when attached with AttachText.
	Text string `json:"text,omitempty"`

	role model.Role
	sym  model.Key
	encl model.Key
}

// Certainty says how sure a result is. Static analysis stays honest: an
// interface call reaches one of its implementations, which the index cannot
// pick, so those are candidates, never facts.
type Certainty string

const (
	Certain   Certainty = "certain"
	Candidate Certainty = "candidate" // possible, from interface satisfaction
	Unknown   Certainty = "unknown"   // the target cannot be determined statically
)

// CallEdge groups the call sites between one caller and one callee.
type CallEdge struct {
	Caller     SymbolRef   `json:"caller"`
	Callee     *SymbolRef  `json:"callee,omitempty"` // nil when the target is unknown
	Dispatch   string      `json:"dispatch"`
	Certainty  Certainty   `json:"certainty"`
	Via        string      `json:"via,omitempty"` // for a candidate: the interface method it was found through
	Sites      []Occ       `json:"sites"`
	Candidates []SymbolRef `json:"candidates,omitempty"` // for an interface call: possible concrete targets
	Note       string      `json:"note,omitempty"`
}
