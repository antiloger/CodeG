import { describe, expect, it } from "vitest"

import type { GraphNode, SymbolRef } from "@/api/types"
import { crumbs, openNode, openSymbol } from "@/lib/navigate"
import { defaults, type ViewState } from "@/lib/url-state"

const node = (over: Partial<GraphNode>): GraphNode => ({ id: "m/a.F", label: "F", kind: "func", ...over })
const pkgView: ViewState = { ...defaults, view: "package", key: "m/a", tests: true }
const fnView: ViewState = { ...defaults, view: "function", key: "m/a.F", pkg: "m/a" }

describe("openNode", () => {
  it("opens a package from the package graph, but nothing else there", () => {
    const next = openNode(defaults, node({ id: "m/a", kind: "package" }))!
    expect(next).toMatchObject({ view: "package", key: "m/a" })
    expect(openNode(defaults, node({ id: "ext:std", kind: "module" }))).toBeUndefined()
  })

  it("opens a function of the package as a function graph, keeping filters", () => {
    const next = openNode(pkgView, node({ location: { package: "m/a", file: "a.go", line: 1, col: 1, endLine: 1, endCol: 2 } }))!
    expect(next).toMatchObject({ view: "function", key: "m/a.F", pkg: "m/a", tests: true, sel: "" })
  })

  it("does not open types, variables, dependencies or unknown calls", () => {
    expect(openNode(pkgView, node({ kind: "type" }))).toBeUndefined()
    expect(openNode(pkgView, node({ kind: "var" }))).toBeUndefined()
    expect(openNode(fnView, node({ kind: "callee", external: true }))).toBeUndefined()
    expect(openNode(fnView, node({ id: "unknown:3", kind: "unknown" }))).toBeUndefined()
  })

  it("opens a callee's own function graph from a function view", () => {
    const next = openNode(fnView, node({ id: "m/b.G", kind: "callee", group: "m/b" }))!
    expect(next).toMatchObject({ view: "function", key: "m/b.G", pkg: "m/b" })
  })
})

describe("openSymbol", () => {
  const sym = (kind: string, over: Partial<SymbolRef> = {}): SymbolRef => ({ key: "m/a.X", package: "m/a", name: "X", kind, ...over })

  it("opens functions as graphs and everything else in its package with the symbol selected", () => {
    expect(openSymbol(defaults, sym("func"))).toMatchObject({ view: "function", key: "m/a.X", pkg: "m/a", sel: "m/a.X" })
    expect(openSymbol(defaults, sym("type"))).toMatchObject({ view: "package", key: "m/a", sel: "m/a.X" })
    expect(openSymbol(defaults, sym("package", { key: "m/a" }))).toMatchObject({ view: "package", key: "m/a", sel: "" })
  })

  it("does not open a dependency's function as a graph", () => {
    expect(openSymbol(defaults, sym("func", { external: true, package: "fmt", key: "fmt.Sprint" }))).toMatchObject({ view: "package", key: "fmt", sel: "fmt.Sprint" })
  })
})

describe("crumbs", () => {
  it("builds the trail for each view", () => {
    expect(crumbs(defaults)).toEqual([{ label: "Packages" }])
    const p = crumbs(pkgView)
    expect(p.map((c) => c.label)).toEqual(["Packages", "m/a"])
    expect(p[0].to?.view).toBe("packages")
    const f = crumbs(fnView)
    expect(f.map((c) => c.label)).toEqual(["Packages", "m/a", "F"])
    expect(f[1].to).toMatchObject({ view: "package", key: "m/a" })
    expect(f[2].to).toBeUndefined() // the current page is not a link
  })

  it("recovers the package from the key when the URL has no pkg", () => {
    const c = crumbs({ ...fnView, pkg: "" })
    expect(c.map((x) => x.label)).toEqual(["Packages", "m/a", "F"])
  })
})
