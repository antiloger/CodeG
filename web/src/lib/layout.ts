import type { ElkNode, ElkExtendedEdge } from "elkjs/lib/elk-api"

import type { Graph, GraphEdge, GraphNode } from "@/api/types"
import type { Direction } from "@/lib/url-state"

// Layout is two pure steps around the (worker-run) ELK call:
//   buildElk   Graph -> ELK compound graph
//   placeNodes ELK result -> positioned nodes and edges for React Flow

export const NODE_HEIGHT = 44
export const NODE_HEIGHT_WITH_DETAIL = 58
const CHAR_WIDTH = 7.4

export function nodeSize(n: GraphNode): { width: number; height: number } {
  const label = n.label.length
  const width = Math.min(340, Math.max(150, Math.round(label * CHAR_WIDTH + 70)))
  return { width, height: n.detail && n.kind !== "package" ? NODE_HEIGHT_WITH_DETAIL : NODE_HEIGHT }
}

export const groupId = (g: string) => `group:${g}`

export interface PlacedNode {
  id: string
  type: "symbol" | "cluster"
  x: number
  y: number
  width: number
  height: number
  parentId?: string
  node?: GraphNode // symbol nodes
  label?: string // group nodes
  own?: boolean // group node: the package being viewed
}

export interface PlacedEdge extends GraphEdge {
  label: string
}

export interface Layout {
  nodes: PlacedNode[] // parents before children
  edges: PlacedEdge[]
}

export interface BuildOptions {
  direction: Direction
  hidden: ReadonlySet<string>
  // Leave out nodes that have no visible edge (package views).
  hideIsolated?: boolean
}

// visibleEdges drops user-hidden kinds, self-loops and edges whose ends are not
// in the slice (a truncated graph can leave some).
export function visibleEdges(g: Graph, hidden: ReadonlySet<string>): GraphEdge[] {
  const ids = new Set(g.nodes.map((n) => n.id))
  return g.edges.filter(
    (e) => !hidden.has(e.kind) && e.source !== e.target && ids.has(e.source) && ids.has(e.target)
  )
}

// Function views are laid out flat; the other levels group nodes by package or
// directory as compound nodes.
export function usesGroups(g: Graph): boolean {
  return g.level !== "function"
}

export function edgeLabel(e: GraphEdge, level: Graph["level"]): string {
  if (level === "function" && e.orders?.length) {
    const shown = e.orders.slice(0, 4).join(", ")
    return e.orders.length > 4 ? `${shown}…` : shown
  }
  return e.count > 1 ? `×${e.count}` : ""
}

export function groupLabel(g: Graph, group: string): { label: string; own: boolean } {
  if (g.level === "package") {
    if (group === "imported") return { label: "imported packages (double-click to expand)", own: false }
    return group === g.root
      ? { label: `this package · ${group}`, own: true }
      : { label: group, own: false }
  }
  return { label: group, own: false }
}

export function buildElk(g: Graph, o: BuildOptions): ElkNode {
  const edges = visibleEdges(g, o.hidden)
  const grouped = usesGroups(g)

  const leaf = (n: GraphNode): ElkNode => ({ id: n.id, ...nodeSize(n) })
  const children: ElkNode[] = []
  if (grouped) {
    const byGroup = new Map<string, ElkNode[]>()
    for (const n of g.nodes) {
      if (!n.group) {
        children.push(leaf(n))
        continue
      }
      const list = byGroup.get(n.group) ?? []
      list.push(leaf(n))
      byGroup.set(n.group, list)
    }
    for (const [group, members] of [...byGroup].sort(([a], [b]) => a.localeCompare(b))) {
      children.push({
        id: groupId(group),
        children: members,
        layoutOptions: { "elk.padding": "[top=36,left=14,bottom=14,right=14]" },
      })
    }
  } else {
    children.push(...g.nodes.map(leaf))
  }

  return {
    id: "root",
    layoutOptions: {
      "elk.algorithm": "layered",
      "elk.direction": o.direction === "LR" ? "RIGHT" : "DOWN",
      "elk.hierarchyHandling": "INCLUDE_CHILDREN",
      "elk.spacing.nodeNode": "28",
      "elk.layered.spacing.nodeNodeBetweenLayers": "70",
      "elk.layered.spacing.edgeNodeBetweenLayers": "24",
      "elk.layered.crossingMinimization.strategy": "LAYER_SWEEP",
      "elk.layered.nodePlacement.strategy": "BRANDES_KOEPF",
      // The package graph has hundreds of nodes in nested groups. ELK's default
      // effort (7) took about 3 s for 331 packages; a lower one is much faster.
      "elk.layered.thoroughness": "2",
      "elk.padding": "[top=20,left=20,bottom=20,right=20]",
    },
    children,
    edges: edges.map(
      (e): ElkExtendedEdge => ({ id: e.id, sources: [e.source], targets: [e.target] })
    ),
  }
}

// placeNodes reads ELK's result. Child coordinates are relative to their
// parent, which is exactly what React Flow's parentId expects.
export function placeNodes(g: Graph, laid: ElkNode, o: BuildOptions): Layout {
  const byId = new Map(g.nodes.map((n) => [n.id, n]))
  const nodes: PlacedNode[] = []
  const walk = (elk: ElkNode, parent?: string) => {
    for (const c of elk.children ?? []) {
      const base = { x: c.x ?? 0, y: c.y ?? 0, width: c.width ?? 0, height: c.height ?? 0 }
      if (c.id.startsWith("group:")) {
        const group = c.id.slice("group:".length)
        const { label, own } = groupLabel(g, group)
        nodes.push({ id: c.id, type: "cluster", ...base, parentId: parent, label, own })
        walk(c, c.id)
      } else {
        const node = byId.get(c.id)
        if (node) nodes.push({ id: c.id, type: "symbol", ...base, parentId: parent, node })
      }
    }
  }
  walk(laid)
  const edges = visibleEdges(g, o.hidden).map((e) => ({ ...e, label: edgeLabel(e, g.level) }))
  return { nodes, edges }
}


// ---- Packed layout (package and function views) ----------------------------
//
// A layered layout puts every neighbour of a hub in one layer: a function that
// calls 50 things becomes a 50-node column. Instead the nodes are clustered by
// role and each cluster is packed into a compact grid here; ELK only lays out
// the cluster boxes, connected by the aggregated edges between clusters.

export interface Cluster {
  id: string // "group:<name>"
  label: string
  own: boolean
  members: GraphNode[]
}

export interface PackedMember {
  node: GraphNode
  x: number
  y: number
  width: number
  height: number
}

export interface PackedCluster extends Cluster {
  width: number
  height: number
  packed: PackedMember[]
}

export interface Plan {
  packed: boolean
  clusters: PackedCluster[]
  loose: GraphNode[]
  visibleIds: Set<string>
}

const GAP = 14
const MAX_CELL_WIDTH = 250
const PAD = { top: 34, left: 12, right: 12, bottom: 12 }

// clusterOf says which cluster a node belongs to, or undefined for a node that
// stands alone (the function being viewed).
export function clusterOf(g: Graph, n: GraphNode): { name: string; label: string; own: boolean } | undefined {
  if (g.level === "function") {
    switch (n.kind) {
      case "function":
        return undefined
      case "param":
      case "result":
        return { name: "signature", label: "Signature", own: false }
      case "local":
        return { name: "locals", label: "Locals", own: false }
      case "closure":
        return { name: "closures", label: "Closures", own: false }
      case "callee":
      case "candidate":
      case "unknown":
        return { name: "calls", label: "Calls", own: false }
      case "type":
        return { name: "types", label: "Types", own: false }
      case "global":
      case "field":
        return { name: "data", label: "Globals & fields", own: false }
      default:
        return { name: "other", label: "Other", own: false }
    }
  }
  // package view
  if (n.members || n.group === "imported") {
    return { name: "imported", label: "Imported packages (double-click to expand)", own: false }
  }
  if (n.group === g.root) {
    switch (n.kind) {
      case "type":
      case "alias":
        return { name: "own-types", label: "Types", own: true }
      case "func":
      case "method":
        return { name: "own-functions", label: "Functions", own: true }
      case "var":
      case "const":
        return { name: "own-values", label: "Variables & constants", own: true }
      default:
        return { name: "own-other", label: "Other", own: true }
    }
  }
  return { name: `imp:${n.group ?? ""}`, label: n.group ?? "", own: false }
}

// packCluster arranges members in a roughly 5:3 grid of equal cells.
export function packCluster(c: Cluster): PackedCluster {
  const members = [...c.members].sort((a, b) => a.kind.localeCompare(b.kind) || a.label.localeCompare(b.label))
  const sizes = members.map(nodeSize)
  // Long labels are truncated in the node rather than widening every cell.
  const cellW = Math.min(MAX_CELL_WIDTH, Math.max(...sizes.map((s) => s.width)))
  const cellH = Math.max(...sizes.map((s) => s.height))
  // Columns chosen so the block is about 1.7 times wider than tall.
  const cols = Math.max(1, Math.min(6, Math.round(Math.sqrt((members.length * 1.7 * (cellH + GAP)) / (cellW + GAP)))))
  const rows = Math.ceil(members.length / cols)
  const packed = members.map((node, i) => ({
    node,
    x: PAD.left + (i % cols) * (cellW + GAP),
    y: PAD.top + Math.floor(i / cols) * (cellH + GAP),
    width: cellW,
    height: cellH,
  }))
  return {
    ...c,
    packed,
    width: PAD.left + PAD.right + cols * cellW + (cols - 1) * GAP,
    height: PAD.top + PAD.bottom + rows * cellH + (rows - 1) * GAP,
  }
}

// usesPacking: every level except the package graph.
export const usesPacking = (g: Graph) => g.level !== "packages"

export function buildPlan(g: Graph, o: BuildOptions): { elk: ElkNode; plan: Plan } {
  const edges = visibleEdges(g, o.hidden)
  let nodes = g.nodes
  if (o.hideIsolated) {
    const linked = new Set(edges.flatMap((e) => [e.source, e.target]))
    nodes = nodes.filter((n) => linked.has(n.id))
  }
  const visibleIds = new Set(nodes.map((n) => n.id))

  const byCluster = new Map<string, Cluster>()
  const loose: GraphNode[] = []
  const owner = new Map<string, string>() // node id -> cluster id or its own id
  for (const n of nodes) {
    const c = clusterOf(g, n)
    if (!c) {
      loose.push(n)
      owner.set(n.id, n.id)
      continue
    }
    const id = `group:${c.name}`
    const cluster = byCluster.get(id) ?? { id, label: c.label, own: c.own, members: [] }
    cluster.members.push(n)
    byCluster.set(id, cluster)
    owner.set(n.id, id)
  }
  const clusters = [...byCluster.values()].sort((a, b) => a.id.localeCompare(b.id)).map(packCluster)

  // Edges between clusters, once each: they only steer ELK's layering.
  const seen = new Set<string>()
  const elkEdges: ElkExtendedEdge[] = []
  for (const e of edges) {
    const a = owner.get(e.source)
    const b = owner.get(e.target)
    if (!a || !b || a === b || seen.has(`${a}>${b}`)) continue
    seen.add(`${a}>${b}`)
    elkEdges.push({ id: `e:${a}>${b}`, sources: [a], targets: [b] })
  }

  const elk: ElkNode = {
    id: "root",
    layoutOptions: {
      "elk.algorithm": "layered",
      "elk.direction": o.direction === "LR" ? "RIGHT" : "DOWN",
      "elk.spacing.nodeNode": "40",
      "elk.layered.spacing.nodeNodeBetweenLayers": "90",
      "elk.layered.crossingMinimization.strategy": "LAYER_SWEEP",
      "elk.padding": "[top=20,left=20,bottom=20,right=20]",
    },
    children: [
      ...loose.map((n) => ({ id: n.id, ...nodeSize(n) })),
      ...clusters.map((c) => ({ id: c.id, width: c.width, height: c.height })),
    ],
    edges: elkEdges,
  }
  return { elk, plan: { packed: true, clusters, loose, visibleIds } }
}

export function placePacked(g: Graph, laid: ElkNode, plan: Plan, o: BuildOptions): Layout {
  const byId = new Map(plan.clusters.map((c) => [c.id, c]))
  const looseById = new Map(plan.loose.map((n) => [n.id, n]))
  const nodes: PlacedNode[] = []
  for (const c of laid.children ?? []) {
    const cluster = byId.get(c.id)
    if (cluster) {
      nodes.push({
        id: c.id, type: "cluster", x: c.x ?? 0, y: c.y ?? 0, width: cluster.width, height: cluster.height,
        label: cluster.label, own: cluster.own,
      })
      for (const m of cluster.packed) {
        nodes.push({ id: m.node.id, type: "symbol", x: m.x, y: m.y, width: m.width, height: m.height, parentId: c.id, node: m.node })
      }
      continue
    }
    const node = looseById.get(c.id)
    if (node) {
      nodes.push({ id: c.id, type: "symbol", x: c.x ?? 0, y: c.y ?? 0, width: c.width ?? 0, height: c.height ?? 0, node })
    }
  }
  const shown = plan.visibleIds
  const edges = visibleEdges(g, o.hidden)
    .filter((e) => shown.has(e.source) && shown.has(e.target))
    .map((e) => ({ ...e, label: edgeLabel(e, g.level) }))
  return { nodes, edges }
}

// arrangeFunction places a function view without ELK. Its shape is fixed by
// roles, so a direct arrangement is more compact and more predictable than a
// layered layout: what the function takes and declares on the left, the
// function in the middle, what it calls, uses and touches on the right (or
// below it, top to bottom).
const FN_LEFT = ["group:signature", "group:locals"]
const FN_RIGHT = ["group:calls", "group:types", "group:data", "group:closures", "group:other"]
const STACK_GAP = 28
const COLUMN_GAP = 110

export function arrangeFunction(plan: Plan, direction: Direction): ElkNode {
  const byId = new Map(plan.clusters.map((c) => [c.id, c]))
  const pick = (ids: string[]) => ids.map((id) => byId.get(id)).filter((c): c is PackedCluster => !!c)
  const left = pick(FN_LEFT)
  const right = pick(FN_RIGHT)
  const center = plan.loose[0]
  const size = center ? nodeSize(center) : { width: 0, height: 0 }
  const children: ElkNode[] = []
  const stackHeight = (cs: PackedCluster[]) => cs.reduce((h, c) => h + c.height, 0) + Math.max(0, cs.length - 1) * STACK_GAP
  const stackWidth = (cs: PackedCluster[]) => Math.max(0, ...cs.map((c) => c.width))

  if (direction === "LR") {
    const leftW = stackWidth(left)
    const total = Math.max(stackHeight(left), stackHeight(right), size.height)
    const centerX = left.length ? leftW + COLUMN_GAP : 0
    let y = (total - stackHeight(left)) / 2
    for (const c of left) {
      children.push({ id: c.id, x: leftW - c.width, y, width: c.width, height: c.height })
      y += c.height + STACK_GAP
    }
    if (center) children.push({ id: center.id, x: centerX, y: (total - size.height) / 2, ...size })
    y = (total - stackHeight(right)) / 2
    for (const c of right) {
      children.push({ id: c.id, x: centerX + size.width + COLUMN_GAP, y, width: c.width, height: c.height })
      y += c.height + STACK_GAP
    }
  } else {
    const all = [...left, ...right]
    const width = Math.max(stackWidth(all), size.width)
    if (center) children.push({ id: center.id, x: (width - size.width) / 2, y: 0, ...size })
    let y = center ? size.height + COLUMN_GAP / 2 : 0
    for (const c of all) {
      children.push({ id: c.id, x: (width - c.width) / 2, y, width: c.width, height: c.height })
      y += c.height + STACK_GAP
    }
  }
  return { id: "root", children }
}
