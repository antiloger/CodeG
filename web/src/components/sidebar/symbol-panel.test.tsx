import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"

import type { SymbolDetail } from "@/api/types"
import { SymbolPanel } from "@/components/sidebar/symbol-panel"
import { mockApi, renderWithProviders } from "@/test/helpers"
import { defaults, type ViewState } from "@/lib/url-state"

const loc = { package: "m/a", file: "a/a.go", line: 3, col: 6, endLine: 3, endCol: 9 }

const func: SymbolDetail = {
  symbol: { key: "m/a.Run", package: "m/a", name: "Run", kind: "func", detail: "func(s m/a.Store) error", location: loc },
  stats: { definition: 1, call: 2 },
  callers: 2,
  callees: 1,
  source: {
    package: "m/a", file: "a/a.go", startLine: 1, endLine: 5, stale: false,
    lines: [{ n: 3, text: "func Run(s Store) error {", highlight: true }],
  },
  relations: [{ kind: "has_param", symbol: { key: "m/a.Run#param0", package: "m/a", name: "s", kind: "param", detail: "m/a.Store" } }],
  limits: ["interface calls list candidates, not the method that runs"],
}

const variable: SymbolDetail = {
  symbol: { key: "m/a.Run::x@0", package: "m/a", name: "x", kind: "local", detail: "int", location: { ...loc, line: 4 } },
  stats: { definition: 1, read: 2 },
  events: [
    { ...loc, line: 4, role: "definition", text: "x := 1" },
    { ...loc, line: 5, role: "read", text: "return x" },
  ],
  limits: ["events are in source order, not execution order"],
}

const state: ViewState = { ...defaults, view: "package", key: "m/a" }
const noop = () => {}

describe("SymbolPanel", () => {
  it("shows a function with its signature, stats, counts and limits, but no Events tab", async () => {
    mockApi({ "/api/symbol": func })
    renderWithProviders(<SymbolPanel symbol="m/a.Run" state={state} onTab={noop} onSelect={noop} onOpenFunction={noop} />)
    expect(await screen.findByRole("heading", { name: "Run" })).toBeInTheDocument()
    expect(screen.getByText("func(s a.Store) error")).toBeInTheDocument() // import path shortened to its last element
    expect(screen.getByText("call")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /callers/i })).toHaveTextContent("2")
    expect(screen.getByText(/what static analysis cannot tell/i)).toBeInTheDocument()
    expect(screen.getByText(/interface calls list candidates/i)).toBeInTheDocument()
    expect(screen.queryByRole("tab", { name: "Events" })).not.toBeInTheDocument()
  })

  it("opens the function graph of a function, but not of a dependency", async () => {
    mockApi({ "/api/symbol": func })
    const onOpen = vi.fn()
    const { unmount } = renderWithProviders(
      <SymbolPanel symbol="m/a.Run" state={state} onTab={noop} onSelect={noop} onOpenFunction={onOpen} />
    )
    await userEvent.click(await screen.findByRole("button", { name: /open function graph/i }))
    expect(onOpen).toHaveBeenCalledWith("m/a.Run", "m/a")
    unmount()

    mockApi({ "/api/symbol": { ...func, symbol: { ...func.symbol, external: true, location: undefined } } })
    renderWithProviders(<SymbolPanel symbol="fmt.Sprint" state={state} onTab={noop} onSelect={noop} onOpenFunction={onOpen} />)
    await screen.findByRole("heading", { name: "Run" })
    expect(screen.queryByRole("button", { name: /open function graph/i })).not.toBeInTheDocument()
    expect(screen.getByText("dependency")).toBeInTheDocument()
  })

  it("gives a variable an Events tab with its events in order", async () => {
    mockApi({ "/api/symbol": variable })
    renderWithProviders(
      <SymbolPanel symbol="m/a.Run::x@0" state={{ ...state, tab: "events" }} onTab={noop} onSelect={noop} onOpenFunction={noop} />
    )
    const list = await screen.findByRole("list", { name: /events in source order/i })
    const items = within(list).getAllByRole("listitem")
    expect(items).toHaveLength(2)
    expect(items[0]).toHaveTextContent("x := 1")
    expect(items[1]).toHaveTextContent("return x")
    expect(screen.getByRole("tab", { name: "Events" })).toBeInTheDocument()
  })

  it("falls back to Overview when Events is asked for on a symbol without events", async () => {
    mockApi({ "/api/symbol": func })
    renderWithProviders(
      <SymbolPanel symbol="m/a.Run" state={{ ...state, tab: "events" }} onTab={noop} onSelect={noop} onOpenFunction={noop} />
    )
    await screen.findByRole("heading", { name: "Run" })
    expect(screen.getByRole("tab", { name: "Overview" })).toHaveAttribute("aria-selected", "true")
  })

  it("loads callers on demand and reports tab changes", async () => {
    const { calls } = mockApi({
      "/api/symbol": func,
      "/api/callers": {
        edges: [
          {
            caller: { key: "m/a.Main", package: "m/a", name: "Main", kind: "func" },
            callee: func.symbol,
            dispatch: "static",
            certainty: "certain",
            sites: [{ ...loc, line: 9, role: "call", text: "Run(s)" }],
          },
        ],
      },
    })
    const onTab = vi.fn()
    const onSelect = vi.fn()
    renderWithProviders(<SymbolPanel symbol="m/a.Run" state={state} onTab={onTab} onSelect={onSelect} onOpenFunction={noop} />)
    await userEvent.click(await screen.findByRole("button", { name: /callers/i }))
    expect(await screen.findByText("Run(s)")).toBeInTheDocument()
    expect(calls.some((c) => c.startsWith("/api/callers?key=m%2Fa.Run"))).toBe(true)
    await userEvent.click(screen.getByRole("button", { name: "a.Main" }))
    expect(onSelect).toHaveBeenCalledWith("m/a.Main")
    await userEvent.click(screen.getByRole("tab", { name: "Source" }))
    expect(onTab).toHaveBeenCalledWith("source")
  })

  it("shows the API's error for a symbol it cannot load", async () => {
    mockApi({ "/api/symbol": () => ({ status: 404, body: { error: "no such symbol: nope" } }) })
    renderWithProviders(<SymbolPanel symbol="nope" state={state} onTab={noop} onSelect={noop} onOpenFunction={noop} />)
    expect(await screen.findByText(/cannot show this symbol/i)).toBeInTheDocument()
    await waitFor(() => expect(screen.getByText(/no such symbol: nope/)).toBeInTheDocument())
  })
})
