import type { Graph, GraphEdge, GraphNode } from "@/api/types"

// A package view lists every imported symbol the package uses. For a big
// package that is hundreds of nodes, so imported packages are collapsed to one
// node each ("N used") until the user expands them. This is a view over the
// API's graph: the server still sends the used symbols, grouped per package.

export const COLLAPSED_PREFIX = "pkg:"
export const IMPORTED_GROUP = "imported"

export const collapsedId = (group: string) => COLLAPSED_PREFIX + group
export const isCollapsedId = (id: string) => id.startsWith(COLLAPSED_PREFIX)
export const groupOfCollapsed = (id: string) => id.slice(COLLAPSED_PREFIX.length)

// shortPackage keeps the last two path elements: golang.org/x/tools/go/ast -> go/ast.
export function shortPackage(pkg: string): string {
  const parts = pkg.split("/")
  return parts.slice(-2).join("/")
}

export function collapseImported(g: Graph, expand: readonly string[]): Graph {
  if (g.level !== "package" || !g.root) return g
  const all = expand.includes("*")
  const open = new Set(expand)

  const keep: GraphNode[] = []
  const members = new Map<string, GraphNode[]>()
  const to = new Map<string, string>()
  for (const n of g.nodes) {
    const imported = !!n.group && n.group !== g.root
    if (!imported || all || open.has(n.group!)) {
      keep.push(n)
      continue
    }
    const list = members.get(n.group!) ?? []
    list.push(n)
    members.set(n.group!, list)
    to.set(n.id, collapsedId(n.group!))
  }
  if (to.size === 0) return g

  const collapsed: GraphNode[] = [...members].map(([group, list]) => ({
    id: collapsedId(group),
    label: shortPackage(group),
    kind: "package",
    group: IMPORTED_GROUP,
    detail: group,
    external: list.every((m) => m.external),
    count: list.length,
    members: list,
  }))

  const agg = new Map<string, GraphEdge>()
  for (const e of g.edges) {
    const source = to.get(e.source) ?? e.source
    const target = to.get(e.target) ?? e.target
    if (source === target) continue
    const key = [source, target, e.kind, e.dispatch ?? "", e.certainty ?? ""].join("\u0000")
    const prev = agg.get(key)
    if (prev) {
      prev.count += e.count
    } else {
      agg.set(key, {
        ...e,
        source,
        target,
        orders: undefined,
        id: `${source} -${e.kind}${e.dispatch ? "/" + e.dispatch : ""}-> ${target}`,
      })
    }
  }
  return { ...g, nodes: [...keep, ...collapsed], edges: [...agg.values()] }
}
