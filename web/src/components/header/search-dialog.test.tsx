import { screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"

import { SearchDialog } from "@/components/header/search-dialog"
import { mockApi, renderWithProviders } from "@/test/helpers"

const sym = (key: string, kind: string) => ({ key, package: key.split(".")[0], name: key.split(".").pop()!, kind })

describe("SearchDialog", () => {
  it("asks the server once typing pauses, lists every match, and picks one", async () => {
    const { calls } = mockApi({
      "/api/resolve": (url) => ({
        body: {
          matches:
            url.searchParams.get("spec") === "Save"
              ? [sym("m/store.Store.Save", "method"), sym("m/store.memStore.Save", "method")]
              : [],
        },
      }),
    })
    const onPick = vi.fn()
    const onOpenChange = vi.fn()
    renderWithProviders(<SearchDialog open onOpenChange={onOpenChange} onPick={onPick} />)

    const input = screen.getByPlaceholderText(/Search:/)
    expect(screen.getByText(/at least two characters/i)).toBeInTheDocument()
    await userEvent.type(input, "Save")

    // debounced: nothing was requested per keystroke, one request for the final text
    const options = await screen.findAllByRole("option")
    expect(options).toHaveLength(2)
    expect(calls.filter((c) => c.startsWith("/api/resolve"))).toEqual(["/api/resolve?spec=Save"])
    expect(options[0]).toHaveTextContent("m/store.Store.Save")
    expect(options[0]).toHaveTextContent("method")

    // the server did the matching: cmdk must not have filtered anything out
    expect(screen.getByText("2 matches")).toBeInTheDocument()

    await userEvent.click(options[1])
    expect(onPick).toHaveBeenCalledWith(expect.objectContaining({ key: "m/store.memStore.Save" }))
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it("says so when nothing matches", async () => {
    mockApi({ "/api/resolve": () => ({ status: 404, body: { error: "no such symbol: Nope" } }) })
    renderWithProviders(<SearchDialog open onOpenChange={() => {}} onPick={() => {}} />)
    await userEvent.type(screen.getByPlaceholderText(/Search:/), "Nope")
    await waitFor(() => expect(screen.getByText(/no symbol matches/i)).toBeInTheDocument())
  })

  it("does not query for a single character", async () => {
    const { calls } = mockApi({ "/api/resolve": { matches: [] } })
    renderWithProviders(<SearchDialog open onOpenChange={() => {}} onPick={() => {}} />)
    await userEvent.type(screen.getByPlaceholderText(/Search:/), "a")
    await new Promise((r) => setTimeout(r, 350))
    expect(calls).toEqual([])
  })
})
