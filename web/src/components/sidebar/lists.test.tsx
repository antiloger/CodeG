import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"

import type { Occurrence, SourceView, VariableEvent } from "@/api/types"
import { EventsList } from "@/components/sidebar/events-list"
import { OccurrenceList } from "@/components/sidebar/occurrence-list"
import { SourceSnippet } from "@/components/sidebar/source-view"

const loc = { package: "m/a", file: "a/a.go", line: 1, col: 1, endLine: 1, endCol: 2 }

describe("EventsList", () => {
  const events: VariableEvent[] = [
    { ...loc, line: 10, role: "definition", text: "b := a", flowTo: "" },
    { ...loc, line: 11, role: "read", flags: ["in_loop", "in_branch"], text: "bar(b, a)", argIndex: 0, passedTo: { key: "m/a.bar", package: "m/a", name: "bar", kind: "func" } },
    { ...loc, line: 12, role: "write_partial", text: "b.x = 1" },
    { ...loc, line: 13, role: "read", text: "return b", flowTo: "m/a.foo#result0" },
  ]

  it("lists events in the order given, numbered, with role and flag badges", () => {
    render(<EventsList events={events} onFocus={() => {}} onSelect={() => {}} />)
    expect(screen.getByText(/source order, not execution order/i)).toBeInTheDocument()
    const items = within(screen.getByRole("list", { name: /events in source order/i })).getAllByRole("listitem")
    expect(items).toHaveLength(4)
    expect(items[0]).toHaveTextContent("1")
    expect(items[0]).toHaveTextContent("definition")
    expect(items[1]).toHaveTextContent("in loop")
    expect(items[1]).toHaveTextContent("in branch")
    expect(items[2]).toHaveTextContent("write partial")
    expect(items[3]).toHaveTextContent("return b")
  })

  it("links an argument to its call and a flow to its target", async () => {
    const onSelect = vi.fn()
    const onFocus = vi.fn()
    render(<EventsList events={events} onFocus={onFocus} onSelect={onSelect} />)
    await userEvent.click(screen.getByRole("button", { name: /argument 0 of/i }))
    expect(onSelect).toHaveBeenCalledWith("m/a.bar")
    await userEvent.click(screen.getByRole("button", { name: /flows into/i }))
    expect(onSelect).toHaveBeenLastCalledWith("m/a.foo#result0")
    await userEvent.click(screen.getByText("b := a"))
    expect(onFocus).toHaveBeenCalledWith(events[0])
  })
})

describe("OccurrenceList", () => {
  const occs: Occurrence[] = [
    { ...loc, file: "a/a.go", line: 5, col: 2, role: "call", text: "f()" },
    { ...loc, file: "a/a.go", line: 9, col: 4, role: "write", text: "x = 1" },
    { ...loc, file: "b/b.go", line: 3, col: 1, role: "read", flags: ["in_closure"] },
  ]

  it("groups by file with a count and shows each source line", () => {
    render(<OccurrenceList occurrences={occs} onFocus={() => {}} />)
    const a = screen.getByRole("region", { name: "a/a.go" })
    expect(within(a).getAllByRole("button")).toHaveLength(2)
    expect(a).toHaveTextContent("f()")
    expect(a).toHaveTextContent("2")
    expect(screen.getByRole("region", { name: "b/b.go" })).toHaveTextContent("in closure")
  })

  it("reports the clicked occurrence", async () => {
    const onFocus = vi.fn()
    render(<OccurrenceList occurrences={occs} onFocus={onFocus} />)
    await userEvent.click(screen.getByText("x = 1"))
    expect(onFocus).toHaveBeenCalledWith(occs[1])
  })
})

describe("SourceSnippet", () => {
  const view: SourceView = {
    package: "m/a",
    file: "a/a.go",
    startLine: 8,
    endLine: 10,
    lines: [
      { n: 8, text: "func f() {" },
      { n: 9, text: "\tx := 1", highlight: true },
      { n: 10, text: "}" },
    ],
    stale: false,
  }

  it("marks the highlighted line and shows line numbers", () => {
    render(<SourceSnippet view={view} />)
    const rows = screen.getAllByRole("row")
    expect(rows).toHaveLength(3)
    expect(rows[1]).toHaveAttribute("aria-current", "true")
    expect(rows[0]).not.toHaveAttribute("aria-current")
    expect(rows[1]).toHaveTextContent("9")
    expect(screen.getByText("a/a.go")).toBeInTheDocument()
    expect(screen.queryByText(/changed after it was indexed/i)).not.toBeInTheDocument()
  })

  it("warns when the file changed after indexing", () => {
    render(<SourceSnippet view={{ ...view, stale: true, test: true }} />)
    expect(screen.getByText(/changed after it was indexed/i)).toBeInTheDocument()
    expect(screen.getByText("test")).toBeInTheDocument()
  })
})
