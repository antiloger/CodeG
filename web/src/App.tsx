import { useCallback, useEffect, useMemo, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"

import { useGraph, useStatus } from "@/api/hooks"
import type { GraphNode, SymbolRef } from "@/api/types"
import { GraphPane } from "@/components/graph/graph-pane"
import { AppHeader } from "@/components/header/app-header"
import { SearchDialog } from "@/components/header/search-dialog"
import { CollapsedInfo, EmptySide, PackageInfo } from "@/components/sidebar/side-info"
import { SymbolPanel } from "@/components/sidebar/symbol-panel"
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from "@/components/ui/resizable"
import { ScrollArea } from "@/components/ui/scroll-area"
import { useViewState } from "@/hooks/use-view-state"
import { collapseImported, groupOfCollapsed, isCollapsedId } from "@/lib/collapse"
import { crumbs, openNode, openSymbol } from "@/lib/navigate"
import { withView, type Tab, type ViewState } from "@/lib/url-state"

function isTyping(t: EventTarget | null) {
  return t instanceof HTMLElement && (t.isContentEditable || !!t.closest("input, textarea, select"))
}

export function App() {
  const [state, setState] = useViewState()
  const graphQuery = useGraph(state)
  const status = useStatus()
  const qc = useQueryClient()
  const [searching, setSearching] = useState(false)
  const graph = useMemo(
    () => (graphQuery.data ? collapseImported(graphQuery.data, state.expand) : undefined),
    [graphQuery.data, state.expand]
  )

  // A new generation means `codeg index` committed: refetch everything.
  const generation = status.data?.generation
  useEffect(() => {
    if (generation) qc.invalidateQueries({ predicate: (q) => q.queryKey[0] !== "status" })
  }, [generation, qc])

  useEffect(() => {
    document.title = state.view === "packages" ? "codeg" : `${state.key} · codeg`
  }, [state.view, state.key])

  const patch = useCallback(
    (p: Partial<ViewState>, opts?: { replace?: boolean }) => setState((s) => ({ ...s, ...p }), opts),
    [setState]
  )
  const select = useCallback((id: string) => patch({ sel: id, tab: "overview" }, { replace: true }), [patch])
  const selectSymbol = useCallback((key: string) => patch({ sel: key, tab: "overview" }), [patch])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault()
        setSearching(true)
      } else if (e.key === "/" && !isTyping(e.target)) {
        e.preventDefault()
        setSearching(true)
      } else if (e.key === "Escape" && !searching && !isTyping(e.target)) {
        patch({ sel: "" }, { replace: true })
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [patch, searching])

  const open = useCallback(
    (n: GraphNode) => {
      if (isCollapsedId(n.id)) {
        // Double-clicking a collapsed package expands it.
        patch({ expand: [...state.expand, groupOfCollapsed(n.id)], sel: "" })
        return
      }
      const next = openNode(state, n)
      if (next) setState(next)
      else select(n.id)
    },
    [state, setState, select, patch]
  )
  const pick = useCallback((s: SymbolRef) => setState((cur) => openSymbol(cur, s)), [setState])

  const selectedNode = useMemo(() => graph?.nodes.find((n) => n.id === state.sel), [graph, state.sel])
  const isSymbol =
    state.sel !== "" && state.view !== "packages" && !state.sel.startsWith("unknown:") && !isCollapsedId(state.sel)

  return (
    <div className="flex h-svh flex-col bg-background text-foreground">
      <AppHeader
        crumbs={crumbs(state)}
        status={status.data}
        statusError={status.error?.message}
        onNavigate={setState}
        onSearch={() => setSearching(true)}
      />
      <SearchDialog open={searching} onOpenChange={setSearching} onPick={pick} />
      <ResizablePanelGroup orientation="horizontal" className="min-h-0 flex-1">
        <ResizablePanel defaultSize="68%" minSize="30%">
          <GraphPane
            state={state}
            graph={graph}
            loading={graphQuery.isFetching}
            error={graphQuery.error}
            selected={state.sel}
            onSelect={select}
            onOpen={open}
            onChange={patch}
          />
        </ResizablePanel>
        <ResizableHandle withHandle />
        <ResizablePanel defaultSize="32%" minSize="20%" maxSize="60%">
          <ScrollArea className="h-full" aria-label="Details">
            {state.sel === "" ? (
              <EmptySide graph={graph} />
            ) : state.view === "packages" && selectedNode && graph ? (
              <PackageInfo node={selectedNode} graph={graph} onOpen={open} />
            ) : selectedNode?.members ? (
              <CollapsedInfo
                node={selectedNode}
                onSelect={selectSymbol}
                onExpand={() => patch({ expand: [...state.expand, groupOfCollapsed(selectedNode.id)], sel: "" })}
              />
            ) : isSymbol ? (
              <SymbolPanel
                key={state.sel}
                symbol={state.sel}
                state={state}
                onTab={(t: Tab) => patch({ tab: t }, { replace: true })}
                onSelect={selectSymbol}
                onOpenFunction={(key, pkg) => setState(withView(state, "function", key, pkg))}
              />
            ) : (
              <EmptySide graph={graph} />
            )}
          </ScrollArea>
        </ResizablePanel>
      </ResizablePanelGroup>
    </div>
  )
}

export default App
