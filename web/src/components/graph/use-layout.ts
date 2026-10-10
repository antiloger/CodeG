import { useEffect, useState } from "react"

import type { Graph } from "@/api/types"
import type { Layout } from "@/lib/layout"
import { runLayout } from "@/lib/run-layout"
import type { Direction } from "@/lib/url-state"

interface Done {
  graph: Graph
  direction: Direction
  hiddenKey: string
  hideIsolated: boolean
  layout?: Layout
  error?: string
}

// useLayout lays a graph out in the worker. While a new layout is being
// computed the previous one stays on screen, so the canvas does not flash.
export function useLayout(graph: Graph | undefined, direction: Direction, hidden: string[], hideIsolated: boolean) {
  const hiddenKey = hidden.join(",")
  const [done, setDone] = useState<Done>()

  useEffect(() => {
    if (!graph) return
    let cancelled = false
    const opts = { direction, hidden: new Set(hiddenKey ? hiddenKey.split(",") : []), hideIsolated }
    runLayout(graph, opts)
      .then((layout) => {
        if (!cancelled) setDone({ graph, direction, hiddenKey, hideIsolated, layout })
      })
      .catch((err: unknown) => {
        if (!cancelled)
          setDone({ graph, direction, hiddenKey, hideIsolated, error: err instanceof Error ? err.message : String(err) })
      })
    return () => {
      cancelled = true
    }
  }, [graph, direction, hiddenKey, hideIsolated])

  const fresh = done && done.graph === graph && done.direction === direction && done.hiddenKey === hiddenKey && done.hideIsolated === hideIsolated
  return {
    layout: done?.layout,
    graph: done?.graph,
    loading: graph !== undefined && !fresh,
    error: fresh ? done?.error : undefined,
  }
}
