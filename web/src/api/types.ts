// Mirrors the JSON of internal/query and internal/server. Field names are the
// Go json tags.

export interface Location {
  package: string
  file: string
  line: number
  col: number
  endLine: number
  endCol: number
  test?: boolean
  generated?: boolean
}

export interface SymbolRef {
  key: string
  package: string
  name: string
  kind: string
  detail?: string
  location?: Location
  external?: boolean
  module?: string
  version?: string
}

export type Certainty = "certain" | "candidate" | "unknown"

export interface Occurrence extends Location {
  symbol?: string
  role: string
  dispatch?: string
  flags?: string[]
  enclosing?: string
  call?: number
  argOf?: number
  argIndex?: number
  flowTo?: string
  typeArgs?: string
  text?: string
}

export interface VariableEvent extends Occurrence {
  passedTo?: SymbolRef
  param?: string
}

export interface CallEdge {
  caller: SymbolRef
  callee?: SymbolRef
  dispatch: string
  certainty: Certainty
  via?: string
  sites: Occurrence[]
  candidates?: SymbolRef[]
  note?: string
}

export interface GraphNode {
  id: string
  label: string
  kind: string
  group?: string
  parent?: string
  detail?: string
  exported?: boolean
  test?: boolean
  generated?: boolean
  external?: boolean
  errors?: boolean
  count?: number
  location?: Location
  // Client-side: the imported symbols a collapsed package node stands for.
  members?: GraphNode[]
}

export interface GraphEdge {
  id: string
  source: string
  target: string
  kind: string
  count: number
  dispatch?: string
  certainty?: Certainty
  orders?: number[]
}

export type GraphLevel = "packages" | "package" | "function"

export interface Graph {
  level: GraphLevel
  root?: string
  nodes: GraphNode[]
  edges: GraphEdge[]
  truncated?: boolean
  notes?: string[]
}

export interface SourceLine {
  n: number
  text: string
  highlight?: boolean
}

export interface SourceView {
  package: string
  file: string
  startLine: number
  endLine: number
  lines: SourceLine[]
  highlight?: Location
  stale: boolean
  test?: boolean
  generated?: boolean
  truncated?: boolean
}

export interface Relation {
  kind: string
  symbol: SymbolRef
}

export interface Implementer {
  type: SymbolRef
  viaPointer?: boolean
  viaEmbedding?: boolean
  interface?: boolean
}

export interface Implemented {
  interface: SymbolRef
  viaPointer?: boolean
  viaEmbedding?: boolean
}

export interface ImplementsResult {
  symbol: SymbolRef
  isInterface: boolean
  implementers?: Implementer[]
  implemented?: Implemented[]
  limits: string
}

export interface SymbolDetail {
  symbol: SymbolRef
  source?: SourceView
  stats: Record<string, number>
  relations?: Relation[]
  callers?: number
  callees?: number
  implements?: ImplementsResult
  events?: VariableEvent[]
  limits?: string[]
}

export interface Status {
  root: string
  config: { GoVersion: string; GOOS: string; GOARCH: string; CGO: string; Tags: string[] | null }
  packages: number
  files: number
  excluded: number
  withErrors: number
  indexedAt: string
  generation: string
}
