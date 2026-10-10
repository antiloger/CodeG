import type { ReactElement } from "react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { render } from "@testing-library/react"
import { vi } from "vitest"

import { TooltipProvider } from "@/components/ui/tooltip"

export function renderWithProviders(ui: ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 0 } } })
  return render(
    <QueryClientProvider client={client}>
      <TooltipProvider>{ui}</TooltipProvider>
    </QueryClientProvider>
  )
}

type Handler = (url: URL) => { status?: number; body: unknown }
type Route = Handler | object

// mockApi replaces fetch with a router keyed by path. A route is a JSON body
// or a function returning {status, body}. Unrouted requests fail the test.
export function mockApi(routes: Record<string, Route>) {
  const calls: string[] = []
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(String(input), "http://localhost")
    calls.push(url.pathname + url.search)
    const route = routes[url.pathname]
    if (route === undefined) throw new Error(`unexpected request: ${url.pathname}${url.search}`)
    const r = typeof route === "function" ? (route as Handler)(url) : { body: route }
    const status = r.status ?? 200
    return new Response(JSON.stringify(r.body), {
      status,
      headers: { "Content-Type": "application/json" },
    })
  })
  vi.stubGlobal("fetch", fetchMock)
  return { calls, fetchMock }
}
