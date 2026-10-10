package model

// PackageFacts is everything one package owns: the unit the extractor
// produces and the index stores. Re-indexing a package replaces all of it.
// It is plain data with no pointers into go/types, so it can be serialized,
// compared and golden-tested.
type PackageFacts struct {
	Path        string
	Name        string
	HasErrors   bool
	Diagnostics []Diagnostic
	Files       []FileFact   // sorted by path; File indexes refer to this slice
	Symbols     []Symbol     // sorted by key
	Occurrences []Occurrence // sorted by file, then position
	Edges       []Edge       // sorted by kind, from, to
	MethodSets  []MethodSet  // sorted by type key
	Externals   []External   // sorted by key; reference-only stubs
	Imports     []string     // import paths, sorted
}

type Diagnostic struct {
	File string
	Line int
	Col  int
	Msg  string
}

type FileFact struct {
	Path      string
	Generated bool
	Test      bool
}

// Range is a source range. Offsets are bytes; lines and columns are 1-based
// and positions ignore //line directives.
type Range struct {
	File    int32
	Start   int32
	End     int32
	Line    int32
	Col     int32
	EndLine int32
	EndCol  int32
}

type Symbol struct {
	Key      Key
	Kind     Kind
	Name     string
	Detail   string // signature for funcs/vars/fields/consts, underlying kind for types
	Exported bool
	Pos      Range // the defining identifier (the `func` keyword for closures)
	Decl     Range // whole declaration for funcs, types, vars and closures; zero otherwise
}

type Role uint8

const (
	RoleInvalid      Role = iota
	RoleDefinition        // declares the symbol
	RoleRead              // value is read
	RoleWrite             // whole overwrite: x = ..., x := ... (redeclared), keyed field in a literal
	RoleReadWrite         // x++, x += 1
	RoleWritePartial      // x.f = ..., x[i] = ...: partial mutation of the root or of an enclosing field
	RoleAddressTaken      // &x, or the implicit &x of a pointer-receiver call
	RoleCall              // callee of a call
	RoleValueRef          // function used as a value
	RoleConstruct         // T{}, &T{}, new(T), make(T)
	RoleTypeUse           // type named in a type position
	RoleImport            // import declaration
)

var roleNames = [...]string{
	RoleInvalid: "invalid", RoleDefinition: "definition", RoleRead: "read", RoleWrite: "write",
	RoleReadWrite: "read_write", RoleWritePartial: "write_partial", RoleAddressTaken: "address_taken",
	RoleCall: "call", RoleValueRef: "value_ref", RoleConstruct: "construct", RoleTypeUse: "type_use",
	RoleImport: "import",
}

func (r Role) String() string {
	if int(r) < len(roleNames) {
		return roleNames[r]
	}
	return "invalid"
}

// Dispatch says how a call reaches its target. Unknown targets are recorded
// as Dynamic with an empty symbol, never guessed.
type Dispatch uint8

const (
	DispatchNone Dispatch = iota
	DispatchStatic
	DispatchInterface
	DispatchDynamic
)

func (d Dispatch) String() string {
	switch d {
	case DispatchStatic:
		return "static"
	case DispatchInterface:
		return "interface"
	case DispatchDynamic:
		return "dynamic"
	}
	return ""
}

// Flags are the per-occurrence context bits.
type Flags uint16

const (
	FlagInLoop    Flags = 1 << iota // inside a for/range loop
	FlagInBranch                    // inside an if/switch/select branch or a short-circuit operand
	FlagInClosure                   // inside a function literal
	FlagInDefer                     // inside a function literal that is deferred
	FlagInGo                        // inside a function literal that runs as a goroutine
	FlagDefer                       // this call is deferred
	FlagGo                          // this call starts a goroutine
	FlagCaptured                    // variable declared outside the current closure
	FlagInitKey                     // field key in a composite literal
)

var flagNames = []struct {
	f Flags
	s string
}{
	{FlagInLoop, "in_loop"}, {FlagInBranch, "in_branch"}, {FlagInClosure, "in_closure"},
	{FlagInDefer, "in_defer"}, {FlagInGo, "in_go"}, {FlagDefer, "defer"}, {FlagGo, "go"},
	{FlagCaptured, "captured"}, {FlagInitKey, "init_key"},
}

// Occurrence is the primary fact: a symbol appearing at a place, in a role.
// "Calls", "callers", "reads" and so on are queries over occurrences.
type Occurrence struct {
	Range
	Symbol    Key // empty for a call whose target is unknown
	Role      Role
	Dispatch  Dispatch // calls only
	Flags     Flags
	Enclosing Key   // function, closure or package-level variable containing the occurrence; the package for top-level declarations
	Call      int32 // calls: call-site ID, unique within the package (1-based)
	ArgOf     int32 // call-site ID this occurrence is a direct argument of, 0 if none
	ArgIndex  int32 // argument position when ArgOf is set
	FlowTo    Key   // statement-level target: `a := b` gives b FlowTo a; `return z` gives z FlowTo the result
	TypeArgs  string
}

type EdgeKind uint8

const (
	EdgeContains  EdgeKind = iota + 1 // package -> top-level symbol, function -> local/closure
	EdgeHasParam                      // function -> parameter or receiver
	EdgeHasResult                     // function -> result
	EdgeHasField                      // struct type or field -> field
	EdgeHasMethod                     // type -> declared method
	EdgeEmbeds                        // type -> embedded type
	EdgeTypeParam                     // func or type -> type parameter
	EdgeAliasOf                       // alias -> named type it denotes
	EdgeFile                          // package -> file
)

var edgeNames = [...]string{
	EdgeContains: "contains", EdgeHasParam: "has_param", EdgeHasResult: "has_result",
	EdgeHasField: "has_field", EdgeHasMethod: "has_method", EdgeEmbeds: "embeds",
	EdgeTypeParam: "type_param", EdgeAliasOf: "alias_of", EdgeFile: "file",
}

func (k EdgeKind) String() string {
	if int(k) < len(edgeNames) && edgeNames[k] != "" {
		return edgeNames[k]
	}
	return "invalid"
}

type Edge struct {
	Kind EdgeKind
	From Key
	To   Key
}

// MethodSet is a type's full method set, flattened including promoted
// methods, so "implements" is a set-inclusion check on canonical strings.
type MethodSet struct {
	Type      Key
	Interface bool
	Methods   []MethodEntry // sorted by name
}

type MethodEntry struct {
	Name    string // qualified with the package path when unexported
	Sig     string // canonical signature without receiver
	PtrRecv bool   // only in the method set of *T
	// Promoted is set when the method comes from an embedded field rather
	// than being declared on the type itself.
	Promoted bool
}

// External is a reference-only stub for a symbol owned by another package.
// The use sites are the occurrences whose Symbol is Key.
type External struct {
	Key     Key
	Kind    Kind
	Name    string
	Detail  string
	Module  string // module path
	Version string
}

// Names returns the names of the set flags, in a fixed order.
func (f Flags) Names() []string {
	var out []string
	for _, fl := range flagNames {
		if f&fl.f != 0 {
			out = append(out, fl.s)
		}
	}
	return out
}
