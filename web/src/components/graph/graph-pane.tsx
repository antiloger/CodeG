import { ReactFlowProvider } from "@xyflow/react"
import { Info, TriangleAlert } from "lucide-react"

import type { Graph, GraphNode } from "@/api/types"
import { GraphCanvas } from "@/components/graph/graph-canvas"
import { GraphToolbar } from "@/components/graph/graph-toolbar"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import type { ViewState } from "@/lib/url-state"

interface Props {
  state: ViewState
  graph: Graph | undefined
  loading: boolean
  error?: Error | null
  selected: string
  onSelect: (id: string) => void
  onOpen: (n: GraphNode) => void
  onChange: (patch: Partial<ViewState>) => void
}

// GraphPane is the left side: toolbar, notes and the canvas, sharing one
// React Flow provider.
export function GraphPane({ state, graph, loading, error, selected, onSelect, onOpen, onChange }: Props) {
  const present = [...new Set(graph?.edges.map((e) => e.kind) ?? [])]
  const notes = (graph?.notes ?? []).filter((n) => !n.startsWith("methods are folded"))
  return (
    <ReactFlowProvider>
      <div className="flex size-full min-w-0 flex-col">
        <GraphToolbar state={state} present={present} onChange={onChange} />
        {(graph?.truncated || notes.length > 0) && (
          <div className="flex flex-col gap-1 border-b px-3 py-1.5" aria-label="Notes">
            {graph?.truncated && (
              <div className="flex items-center gap-2 text-xs text-foreground">
                <TriangleAlert className="size-3.5 text-destructive" aria-hidden />
                <span>
                  Showing part of a large graph. Raise “Max nodes”, hide edge kinds, or open a single function.
                </span>
              </div>
            )}
            {notes.map((n) => (
              <div key={n} className="flex items-center gap-2 text-xs text-muted-foreground">
                <Info className="size-3.5" aria-hidden />
                <span>{n}</span>
              </div>
            ))}
          </div>
        )}
        <div className="min-h-0 flex-1">
          {error && !graph ? (
            <div className="p-6">
              <Alert variant="destructive">
                <AlertTitle>Cannot load this view</AlertTitle>
                <AlertDescription>{error.message}</AlertDescription>
              </Alert>
            </div>
          ) : graph && graph.nodes.length === 0 ? (
            <div className="p-8 text-sm text-muted-foreground">
              Nothing to show. Try enabling tests or generated files, or a different package.
            </div>
          ) : (
            <GraphCanvas
              graph={graph}
              loading={loading}
              direction={state.dir}
              hidden={state.hide}
              hideIsolated={state.view === "package" && !state.isolated}
              selected={selected}
              onSelect={onSelect}
              onOpen={onOpen}
            />
          )}
        </div>
      </div>
    </ReactFlowProvider>
  )
}
