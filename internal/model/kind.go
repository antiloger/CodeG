package model

// Kind classifies a symbol. The set follows the graph model in PLAN.md §4.
type Kind uint8

const (
	KindInvalid Kind = iota
	KindPackage
	KindFile
	KindConst
	KindVar // package-level variable
	KindField
	KindFunc
	KindMethod
	KindType // named type: struct, interface or other
	KindAlias
	KindTypeParam
	KindLocal
	KindParam // includes the receiver
	KindResult
	KindFuncLit
	KindLabel
)

var kindNames = [...]string{
	KindInvalid:   "invalid",
	KindPackage:   "package",
	KindFile:      "file",
	KindConst:     "const",
	KindVar:       "var",
	KindField:     "field",
	KindFunc:      "func",
	KindMethod:    "method",
	KindType:      "type",
	KindAlias:     "alias",
	KindTypeParam: "type_param",
	KindLocal:     "local",
	KindParam:     "param",
	KindResult:    "result",
	KindFuncLit:   "func_lit",
	KindLabel:     "label",
}

func (k Kind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return "invalid"
}
