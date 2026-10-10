import { describe, expect, it } from "vitest"

import type { Graph, GraphNode } from "@/api/types"
import { collapseImported, collapsedId, groupOfCollapsed, isCollapsedId, shortPackage } from "@/lib/collapse"

const n = (id: string, group: string, over: Partial<GraphNode> = {}): GraphNode => ({
  id,
  label: id.split(".").pop()!,
  kind: "func",
  group,
  ...over,
})

const g: Graph = {
  level: "package",
  root: "m/a",
  nodes: [
    n("m/a.F", "m/a"),
    n("m/a.G", "m/a"),
    n("fmt.Sprint", "fmt", { external: true }),
    n("fmt.Println", "fmt", { external: true }),
    n("m/b.Thing", "m/b"),
  ],
  edges: [
    { id: "1", source: "m/a.F", target: "fmt.Sprint", kind: "call", count: 2 },
    { id: "2", source: "m/a.F", target: "fmt.Println", kind: "call", count: 3 },
    { id: "3", source: "m/a.G", target: "fmt.Println", kind: "call", count: 1 },
    { id: "4", source: "m/a.F", target: "fmt.Sprint", kind: "read", count: 1 },
    { id: "5", source: "m/a.F", target: "m/a.G", kind: "call", count: 1 },
    { id: "6", source: "fmt.Sprint", target: "fmt.Println", kind: "call", count: 1 }, // inside one package
    { id: "7", source: "m/a.F", target: "m/b.Thing", kind: "type", count: 1 },
  ],
}

describe("collapseImported", () => {
  it("replaces each imported package's symbols with one node and re-aggregates edges", () => {
    const out = collapseImported(g, [])
    expect(out.nodes.map((x) => x.id).sort()).toEqual(["m/a.F", "m/a.G", "pkg:fmt", "pkg:m/b"])
    const fmt = out.nodes.find((x) => x.id === "pkg:fmt")!
    expect(fmt).toMatchObject({ kind: "package", label: "fmt", group: "imported", count: 2, external: true, detail: "fmt" })
    expect(fmt.members?.map((m) => m.id)).toEqual(["fmt.Sprint", "fmt.Println"])
    // 2 + 3 + 1 calls from the package into fmt
    const calls = out.edges.filter((e) => e.target === "pkg:fmt" && e.kind === "call")
    expect(calls).toHaveLength(2) // from F and from G
    expect(calls.find((e) => e.source === "m/a.F")!.count).toBe(5)
    // kinds stay separate; edges inside a collapsed package vanish; own edges are untouched
    expect(out.edges.find((e) => e.kind === "read")).toMatchObject({ source: "m/a.F", target: "pkg:fmt", count: 1 })
    expect(out.edges.some((e) => e.source === e.target)).toBe(false)
    expect(out.edges.find((e) => e.target === "m/a.G")).toBeDefined()
    expect(out.edges.find((e) => e.target === "pkg:m/b")!.kind).toBe("type")
    expect(out.notes).toEqual(g.notes)
  })

  it("expands single packages, or all with *", () => {
    expect(collapseImported(g, ["fmt"]).nodes.map((x) => x.id).sort()).toEqual([
      "fmt.Println",
      "fmt.Sprint",
      "m/a.F",
      "m/a.G",
      "pkg:m/b",
    ])
    const all = collapseImported(g, ["*"])
    expect(all).toBe(g) // nothing to collapse: the same graph
  })

  it("only touches package views", () => {
    const fn: Graph = { ...g, level: "function" }
    expect(collapseImported(fn, [])).toBe(fn)
    const pk: Graph = { ...g, root: undefined }
    expect(collapseImported(pk, [])).toBe(pk)
  })

  it("has helpers for collapsed ids", () => {
    expect(isCollapsedId(collapsedId("a/b"))).toBe(true)
    expect(isCollapsedId("a/b.F")).toBe(false)
    expect(groupOfCollapsed(collapsedId("a/b"))).toBe("a/b")
    expect(shortPackage("golang.org/x/tools/go/ast")).toBe("go/ast")
    expect(shortPackage("fmt")).toBe("fmt")
  })
})
