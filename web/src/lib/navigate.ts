import type { GraphNode, SymbolRef } from "@/api/types"
import { withView, type ViewState } from "@/lib/url-state"

const FUNCTION_KINDS = new Set(["func", "function", "method", "callee", "closure", "candidate"])

// openNode is what double-clicking a node does. It returns the new state, or
// undefined when the node has nothing to open (a type, a variable, a
// dependency's function).
export function openNode(s: ViewState, node: GraphNode): ViewState | undefined {
  if (s.view === "packages") {
    return node.kind === "package" ? withView(s, "package", node.id) : undefined
  }
  if (!FUNCTION_KINDS.has(node.kind) || node.external || node.id.startsWith("unknown:")) return undefined
  const pkg = node.location?.package ?? node.group ?? s.key
  return withView(s, "function", node.id, pkg)
}

// openSymbol is what picking a search result does: a function opens its graph;
// anything else opens its package with the symbol selected.
export function openSymbol(s: ViewState, sym: SymbolRef): ViewState {
  if (["func", "method"].includes(sym.kind) && !sym.external) {
    return { ...withView(s, "function", sym.key, sym.package), sel: sym.key }
  }
  if (sym.kind === "package") return withView(s, "package", sym.key)
  return { ...withView(s, "package", sym.package), sel: sym.key }
}

// crumbs are the breadcrumb trail for a state.
export interface Crumb {
  label: string
  to?: ViewState // undefined for the current page
}

export function crumbs(s: ViewState): Crumb[] {
  const root = withView(s, "packages", "")
  if (s.view === "packages") return [{ label: "Packages" }]
  const list: Crumb[] = [{ label: "Packages", to: root }]
  if (s.view === "package") return [...list, { label: s.key }]
  const pkg = s.pkg || s.key.split(".").slice(0, -1).join(".")
  const fn = s.key.startsWith(pkg + ".") ? s.key.slice(pkg.length + 1) : s.key
  return [...list, { label: pkg, to: withView(s, "package", pkg) }, { label: fn }]
}
