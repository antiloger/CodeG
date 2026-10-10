import { describe, expect, it } from "vitest"

import type { Graph, GraphNode } from "@/api/types"
import { buildElk, buildPlan, clusterOf, edgeLabel, nodeSize, packCluster, placeNodes, visibleEdges } from "@/lib/layout"
import { runLayout } from "@/lib/run-layout"

const node = (id: string, over: Partial<GraphNode> = {}): GraphNode => ({
  id,
  label: id,
  kind: "func",
  ...over,
})

const pkgGraph: Graph = {
  level: "packages",
  root: "m/a",
  nodes: [
    node("m/a.F", { group: "m/a" }),
    node("m/a.G", { group: "m/a" }),
    node("fmt.Sprint", { group: "fmt", external: true }),
  ],
  edges: [
    { id: "1", source: "m/a.F", target: "m/a.G", kind: "call", count: 1 },
    { id: "2", source: "m/a.F", target: "fmt.Sprint", kind: "call", count: 3 },
    { id: "3", source: "m/a.G", target: "m/a.G", kind: "call", count: 1 }, // self loop
    { id: "4", source: "m/a.G", target: "gone", kind: "call", count: 1 }, // dangling
    { id: "5", source: "m/a.F", target: "m/a.G", kind: "read", count: 1 },
  ],
}

describe("visibleEdges", () => {
  it("drops self-loops, dangling edges and hidden kinds", () => {
    const e = visibleEdges(pkgGraph, new Set(["read"]))
    expect(e.map((x) => x.id)).toEqual(["1", "2"])
  })
})

describe("nodeSize", () => {
  it("grows with the label but stays bounded", () => {
    expect(nodeSize(node("a")).width).toBe(150)
    expect(nodeSize(node("x".repeat(200))).width).toBe(340)
    expect(nodeSize(node("f", { detail: "func()" })).height).toBeGreaterThan(nodeSize(node("f")).height)
  })
})

describe("buildElk", () => {
  it("nests nodes in one compound node per group, sorted, and lists visible edges", () => {
    const elk = buildElk(pkgGraph, { direction: "LR", hidden: new Set() })
    expect(elk.layoutOptions?.["elk.direction"]).toBe("RIGHT")
    expect(elk.children?.map((c) => c.id)).toEqual(["group:fmt", "group:m/a"])
    expect(elk.children?.[1].children?.map((c) => c.id)).toEqual(["m/a.F", "m/a.G"])
    expect(elk.edges?.map((e) => e.id)).toEqual(["1", "2", "5"])
  })

  it("lays function views out flat", () => {
    const fn: Graph = { level: "function", nodes: [node("a", { group: "p" }), node("b")], edges: [] }
    const elk = buildElk(fn, { direction: "TB", hidden: new Set() })
    expect(elk.layoutOptions?.["elk.direction"]).toBe("DOWN")
    expect(elk.children?.map((c) => c.id)).toEqual(["a", "b"])
  })

  it("keeps ungrouped nodes at the top level", () => {
    const g: Graph = { level: "packages", nodes: [node("root"), node("x", { group: "d" })], edges: [] }
    const elk = buildElk(g, { direction: "LR", hidden: new Set() })
    expect(elk.children?.map((c) => c.id)).toEqual(["root", "group:d"])
  })
})

describe("edgeLabel", () => {
  it("shows source-order numbers in function views and counts elsewhere", () => {
    const e = { id: "x", source: "a", target: "b", kind: "call", count: 6, orders: [1, 2, 3, 4, 5, 6] }
    expect(edgeLabel(e, "function")).toBe("1, 2, 3, 4…")
    expect(edgeLabel({ ...e, orders: [7] }, "function")).toBe("7")
    expect(edgeLabel(e, "package")).toBe("×6")
    expect(edgeLabel({ ...e, count: 1 }, "package")).toBe("")
  })
})

describe("runLayout (real ELK)", () => {
  it("places every visible node inside its group, children before parents' children", async () => {
    const out = await runLayout(pkgGraph, { direction: "LR", hidden: new Set() })
    const ids = out.nodes.map((n) => n.id)
    expect(ids).toContain("group:m/a")
    // parents come before their children (React Flow requires it)
    for (const n of out.nodes) {
      if (n.parentId) expect(ids.indexOf(n.parentId)).toBeLessThan(ids.indexOf(n.id))
    }
    const own = out.nodes.find((n) => n.id === "group:m/a")!
    expect(own.label).toBe("m/a")
    const f = out.nodes.find((n) => n.id === "m/a.F")!
    expect(f.parentId).toBe("group:m/a")
    // relative coordinates fit inside the parent
    expect(f.x).toBeGreaterThanOrEqual(0)
    expect(f.x + f.width).toBeLessThanOrEqual(own.width)
    expect(f.y + f.height).toBeLessThanOrEqual(own.height)
    expect(out.edges.map((e) => e.id)).toEqual(["1", "2", "5"])
    expect(out.edges.find((e) => e.id === "2")!.label).toBe("×3")
  })

  it("does not overlap siblings", async () => {
    const g: Graph = {
      level: "function",
      nodes: ["a", "b", "c", "d"].map((i) => node(i)),
      edges: [
        { id: "1", source: "a", target: "b", kind: "call", count: 1 },
        { id: "2", source: "a", target: "c", kind: "call", count: 1 },
        { id: "3", source: "a", target: "d", kind: "call", count: 1 },
      ],
    }
    const { nodes } = await runLayout(g, { direction: "LR", hidden: new Set() })
    for (const a of nodes) {
      for (const b of nodes) {
        if (a.id >= b.id || a.parentId !== b.parentId) continue // a cluster and its members overlap by design
        const overlap = a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height
        expect(overlap, `${a.id} overlaps ${b.id}`).toBe(false)
      }
    }
  })
})

describe("placeNodes", () => {
  it("skips ELK nodes it does not know", () => {
    const g: Graph = { level: "function", nodes: [node("a")], edges: [] }
    const out = placeNodes(
      g,
      { id: "root", children: [{ id: "a", x: 1, y: 2, width: 3, height: 4 }, { id: "zzz" }] },
      { direction: "LR", hidden: new Set() }
    )
    expect(out.nodes.map((n) => n.id)).toEqual(["a"])
  })
})


// ---- packed layout (package and function views) ----

const fnGraph: Graph = {
  level: "function",
  root: "m.F",
  nodes: [
    node("m.F", { kind: "function" }),
    node("m.F#param0", { kind: "param" }),
    node("m.F#result0", { kind: "result" }),
    node("m.F::x@0", { kind: "local" }),
    node("m.G", { kind: "callee" }),
    node("fmt.Println", { kind: "callee", external: true }),
    node("m.T", { kind: "type" }),
    node("m.Counter", { kind: "global" }),
    node("unknown:1", { kind: "unknown" }),
  ],
  edges: [
    { id: "1", source: "m.F", target: "m.G", kind: "call", count: 1, orders: [1] },
    { id: "2", source: "m.F", target: "fmt.Println", kind: "call", count: 1, orders: [2] },
    { id: "3", source: "m.F", target: "m.T", kind: "construct", count: 1, orders: [3] },
    { id: "4", source: "m.F", target: "m.Counter", kind: "write", count: 1, orders: [4] },
    { id: "5", source: "m.F", target: "m.F#param0", kind: "has_param", count: 1 },
    { id: "6", source: "m.F", target: "m.F#result0", kind: "has_result", count: 1 },
    { id: "7", source: "m.F", target: "m.F::x@0", kind: "declares", count: 1 },
    { id: "8", source: "m.F", target: "unknown:1", kind: "call", count: 1, orders: [5] },
  ],
}

describe("clusterOf", () => {
  it("groups a function's nodes by role and leaves the function itself alone", () => {
    const names = Object.fromEntries(fnGraph.nodes.map((n) => [n.id, clusterOf(fnGraph, n)?.name]))
    expect(names).toEqual({
      "m.F": undefined,
      "m.F#param0": "signature",
      "m.F#result0": "signature",
      "m.F::x@0": "locals",
      "m.G": "calls",
      "fmt.Println": "calls",
      "m.T": "types",
      "m.Counter": "data",
      "unknown:1": "calls",
    })
  })

  it("splits a package view into own clusters by kind and one cluster per imported package", () => {
    const g: Graph = { level: "package", root: "m/a", nodes: [], edges: [] }
    const of = (n: GraphNode) => clusterOf(g, n)
    expect(of(node("m/a.T", { kind: "type", group: "m/a" }))).toMatchObject({ name: "own-types", own: true })
    expect(of(node("m/a.F", { kind: "func", group: "m/a" }))).toMatchObject({ name: "own-functions", own: true })
    expect(of(node("m/a.C", { kind: "const", group: "m/a" }))?.name).toBe("own-values")
    expect(of(node("fmt.Sprint", { group: "fmt" }))).toMatchObject({ name: "imp:fmt", own: false })
    expect(of(node("pkg:fmt", { group: "imported", members: [] }))?.name).toBe("imported")
  })
})

describe("packCluster", () => {
  it("packs members into a grid that fits inside the cluster without overlap", () => {
    const members = Array.from({ length: 23 }, (_, i) => node(`m.F${i}`, { label: `F${i}` }))
    const c = packCluster({ id: "group:x", label: "X", own: false, members })
    expect(c.packed).toHaveLength(23)
    for (const m of c.packed) {
      expect(m.x).toBeGreaterThanOrEqual(0)
      expect(m.y).toBeGreaterThanOrEqual(34) // room for the label
      expect(m.x + m.width).toBeLessThanOrEqual(c.width)
      expect(m.y + m.height).toBeLessThanOrEqual(c.height)
    }
    for (const a of c.packed) {
      for (const b of c.packed) {
        if (a === b) continue
        const overlap = a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height
        expect(overlap).toBe(false)
      }
    }
    // wider than tall: not a column of 23
    expect(c.width).toBeGreaterThan(c.height * 0.8)
  })

  it("is deterministic and sorts members by kind then label", () => {
    const members = [node("b", { label: "b" }), node("a", { label: "a" }), node("c", { kind: "const", label: "c" })]
    const ids = packCluster({ id: "g", label: "", own: false, members }).packed.map((m) => m.node.id)
    expect(ids).toEqual(["c", "a", "b"])
  })
})

describe("buildPlan", () => {
  it("hands ELK cluster boxes and one aggregated edge per cluster pair", () => {
    const { elk, plan } = buildPlan(fnGraph, { direction: "LR", hidden: new Set(["has_param", "has_result", "declares"]) })
    expect(plan.loose.map((n) => n.id)).toEqual(["m.F"])
    // Signature and Locals stay as (unconnected) boxes: their nodes are not hidden
    expect(elk.children?.map((c) => c.id).sort()).toEqual([
      "group:calls",
      "group:data",
      "group:locals",
      "group:signature",
      "group:types",
      "m.F",
    ])
    // three calls (m.G, fmt.Println, unknown) become one edge to the calls cluster
    const edges = elk.edges?.map((e) => e.id).sort()
    expect(edges).toEqual(["e:m.F>group:calls", "e:m.F>group:data", "e:m.F>group:types"])
  })

  it("hides nodes without a visible edge when asked", () => {
    const hide = new Set(["has_param", "has_result", "declares"])
    const shown = buildPlan(fnGraph, { direction: "LR", hidden: hide }).plan
    expect(shown.visibleIds.has("m.F::x@0")).toBe(true)
    const hidden = buildPlan(fnGraph, { direction: "LR", hidden: hide, hideIsolated: true }).plan
    expect(hidden.visibleIds.has("m.F::x@0")).toBe(false) // its only edge was "declares"
    expect(hidden.visibleIds.has("m.G")).toBe(true)
  })
})

describe("function arrangement", () => {
  it("puts the signature on the left, the function in the middle and what it uses on the right", async () => {
    const out = await runLayout(fnGraph, { direction: "LR", hidden: new Set(["has_param", "has_result", "declares"]) })
    const at = (id: string) => out.nodes.find((n) => n.id === id)!
    expect(at("group:signature").x).toBeLessThan(at("m.F").x)
    expect(at("m.F").x).toBeLessThan(at("group:calls").x)
    // the calls, types and data clusters share one column, stacked without overlap
    const right = ["group:calls", "group:types", "group:data"].map(at)
    expect(new Set(right.map((c) => c.x)).size).toBe(1)
    const sorted = [...right].sort((a, b) => a.y - b.y)
    for (let i = 1; i < sorted.length; i++) expect(sorted[i].y).toBeGreaterThanOrEqual(sorted[i - 1].y + sorted[i - 1].height)
  })

  it("stacks everything below the function when flowing top to bottom", async () => {
    const out = await runLayout(fnGraph, { direction: "TB", hidden: new Set() })
    const f = out.nodes.find((n) => n.id === "m.F")!
    for (const c of out.nodes.filter((n) => n.type === "cluster")) expect(c.y).toBeGreaterThan(f.y + f.height)
  })
})

describe("runLayout on packed views (real ELK)", () => {
  it("places clusters without overlap, members inside their cluster, edges kept", async () => {
    const out = await runLayout(fnGraph, { direction: "LR", hidden: new Set(["has_param", "has_result", "declares"]) })
    const clusters = out.nodes.filter((n) => n.type === "cluster")
    expect(clusters.map((c) => c.label).sort()).toEqual(["Calls", "Globals & fields", "Locals", "Signature", "Types"])
    for (const a of clusters) {
      for (const b of clusters) {
        if (a.id >= b.id) continue
        const overlap = a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height
        expect(overlap, `${a.label} overlaps ${b.label}`).toBe(false)
      }
    }
    const ids = out.nodes.map((n) => n.id)
    for (const n of out.nodes) {
      if (n.parentId) expect(ids.indexOf(n.parentId)).toBeLessThan(ids.indexOf(n.id))
    }
    const g = out.nodes.find((n) => n.id === "m.G")!
    const calls = out.nodes.find((n) => n.id === "group:calls")!
    expect(g.parentId).toBe("group:calls")
    expect(g.x + g.width).toBeLessThanOrEqual(calls.width)
    // the function stands alone, to the left of what it calls (LR)
    const f = out.nodes.find((n) => n.id === "m.F")!
    expect(f.parentId).toBeUndefined()
    expect(f.x).toBeLessThan(calls.x)
    // hidden kinds drop out; the rest keep their source-order labels
    expect(out.edges.map((e) => e.kind).sort()).toEqual(["call", "call", "call", "construct", "write"])
    expect(out.edges.find((e) => e.target === "m.G")!.label).toBe("1")
  })

  it("lays out a 60-callee function as a compact block, not a tall column", async () => {
    const callees = Array.from({ length: 60 }, (_, i) => node(`m.C${i}`, { kind: "callee", label: `C${i}` }))
    const g: Graph = {
      level: "function",
      nodes: [node("m.F", { kind: "function" }), ...callees],
      edges: callees.map((c, i) => ({ id: `e${i}`, source: "m.F", target: c.id, kind: "call", count: 1, orders: [i + 1] })),
    }
    const { nodes } = await runLayout(g, { direction: "LR", hidden: new Set() })
    const calls = nodes.find((n) => n.id === "group:calls")!
    expect(calls.height).toBeLessThan(calls.width) // wider than tall
    expect(calls.height).toBeLessThan(700)
    expect(calls.width).toBeLessThan(1200)
  })
})
