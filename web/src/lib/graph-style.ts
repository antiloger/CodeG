// How kinds look. Colors are the theme's own tokens (index.css), so the graph
// follows light/dark and any retheming without a palette of its own.

export interface EdgeStyle {
  label: string
  color: string // a CSS color expression over theme tokens
  dash?: string // SVG dash pattern
  structural?: boolean // ownership edges: drawn quietly
}

// The theme's chart-1..5 tokens are one hue in five lightnesses, so they cannot
// tell edge kinds apart. Kinds are distinguished by the theme's genuinely
// different colors (primary, destructive, foreground, muted) and by dash
// pattern, and the toolbar legend draws the same line sample.
const FG_SOFT = "color-mix(in oklab, var(--foreground) 55%, transparent)"

export const EDGE_STYLES: Record<string, EdgeStyle> = {
  import: { label: "imports", color: "var(--muted-foreground)" },
  call: { label: "calls", color: "var(--primary)" },
  ref: { label: "function value", color: "var(--primary)", dash: "6 4" },
  read: { label: "reads", color: FG_SOFT },
  write: { label: "writes / may modify", color: "var(--destructive)" },
  type: { label: "uses type", color: "var(--muted-foreground)", dash: "2 4" },
  construct: { label: "constructs", color: "var(--foreground)", dash: "8 3 2 3" },
  flow: { label: "value flow", color: "var(--primary)", dash: "2 3" },
  may_call: { label: "may call (candidate)", color: "var(--primary)", dash: "6 4" },
  embeds: { label: "embeds", color: "var(--foreground)" },
  has_method: { label: "has method", color: "var(--muted-foreground)", structural: true },
  contains: { label: "contains", color: "var(--muted-foreground)", structural: true },
  declares: { label: "declares", color: "var(--muted-foreground)", structural: true },
  has_param: { label: "parameter", color: "var(--muted-foreground)", structural: true },
  has_result: { label: "result", color: "var(--muted-foreground)", structural: true },
}

const FALLBACK: EdgeStyle = { label: "", color: "var(--muted-foreground)" }

export function edgeStyle(kind: string): EdgeStyle {
  return EDGE_STYLES[kind] ?? { ...FALLBACK, label: kind }
}

// Edge kinds offered as filters, per view, in display order.
export const FILTERABLE: Record<string, string[]> = {
  packages: ["import"],
  package: ["call", "read", "write", "type", "construct", "ref", "embeds", "has_method"],
  function: ["call", "read", "write", "type", "construct", "flow", "may_call", "contains", "declares", "has_param", "has_result"],
}

export interface KindStyle {
  label: string
  // Badge variant from the shadcn badge component.
  variant: "default" | "secondary" | "outline" | "destructive"
}

const KIND_LABELS: Record<string, KindStyle> = {
  package: { label: "package", variant: "secondary" },
  module: { label: "module", variant: "outline" },
  func: { label: "func", variant: "default" },
  function: { label: "func", variant: "default" },
  method: { label: "method", variant: "default" },
  callee: { label: "func", variant: "secondary" },
  closure: { label: "closure", variant: "secondary" },
  type: { label: "type", variant: "secondary" },
  alias: { label: "alias", variant: "secondary" },
  const: { label: "const", variant: "outline" },
  var: { label: "var", variant: "outline" },
  global: { label: "var", variant: "outline" },
  field: { label: "field", variant: "outline" },
  local: { label: "local", variant: "outline" },
  param: { label: "param", variant: "outline" },
  result: { label: "result", variant: "outline" },
  type_param: { label: "type param", variant: "outline" },
  label: { label: "label", variant: "outline" },
  candidate: { label: "candidate", variant: "outline" },
  unknown: { label: "unknown", variant: "destructive" },
  universe: { label: "builtin", variant: "outline" },
}

export function kindStyle(kind: string): KindStyle {
  return KIND_LABELS[kind] ?? { label: kind, variant: "outline" }
}
