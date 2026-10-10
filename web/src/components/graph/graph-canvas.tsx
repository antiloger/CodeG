import { useEffect, useMemo } from "react"
import {
  Background,
  Controls,
  MarkerType,
  MiniMap,
  ReactFlow,
  useReactFlow,
  type NodeMouseHandler,
} from "@xyflow/react"
import "@xyflow/react/dist/style.css"

import type { Graph, GraphNode } from "@/api/types"
import { GroupNode, type GroupFlowNode } from "@/components/graph/group-node"
import { LabeledEdge, type LabeledFlowEdge } from "@/components/graph/labeled-edge"
import { SymbolNode, type SymbolFlowNode } from "@/components/graph/symbol-node"
import { useLayout } from "@/components/graph/use-layout"
import { useTheme } from "@/components/theme-provider"
import { Skeleton } from "@/components/ui/skeleton"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { edgeStyle } from "@/lib/graph-style"
import type { Direction } from "@/lib/url-state"

// Not named "group": React Flow styles a built-in node type of that name.
const nodeTypes = { symbol: SymbolNode, cluster: GroupNode }
const edgeTypes = { labeled: LabeledEdge }

interface Props {
  graph: Graph | undefined
  loading: boolean
  direction: Direction
  hidden: string[]
  hideIsolated: boolean
  selected: string
  onSelect: (id: string) => void
  onOpen: (node: GraphNode) => void
}

export function GraphCanvas({ graph, loading, direction, hidden, hideIsolated, selected, onSelect, onOpen }: Props) {
  const { layout, error, loading: laying } = useLayout(graph, direction, hidden, hideIsolated)
  const { theme } = useTheme()
  const rf = useReactFlow()

  const { nodes, edges } = useMemo(() => {
    if (!layout) return { nodes: [] as (SymbolFlowNode | GroupFlowNode)[], edges: [] as LabeledFlowEdge[] }
    const touching = new Set<string>()
    if (selected) {
      for (const e of layout.edges) {
        if (e.source === selected || e.target === selected) {
          touching.add(e.source)
          touching.add(e.target)
        }
      }
    }
    const focus = selected !== "" && touching.size > 0
    const nodes = layout.nodes.map((p): SymbolFlowNode | GroupFlowNode => {
      const base = {
        id: p.id,
        position: { x: p.x, y: p.y },
        parentId: p.parentId,
        // Explicit sizes let the minimap and fit-view work before React Flow measures.
        width: p.width,
        height: p.height,
        style: { width: p.width, height: p.height },
      }
      if (p.type === "cluster") {
        return { ...base, type: "cluster", data: { label: p.label ?? "", own: !!p.own }, selectable: false, draggable: false, zIndex: -1 }
      }
      return {
        ...base,
        type: "symbol",
        selected: p.id === selected,
        data: { node: p.node!, direction, dim: focus && !touching.has(p.id) },
      }
    })
    const edges = layout.edges.map(
      (e): LabeledFlowEdge => ({
        id: e.id,
        source: e.source,
        target: e.target,
        type: "labeled",
        markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14, color: edgeStyle(e.kind).color },
        data: {
          kind: e.kind,
          label: e.label,
          count: e.count,
          certainty: e.certainty,
          dim: focus && e.source !== selected && e.target !== selected,
        },
      })
    )
    return { nodes, edges }
  }, [layout, selected, direction])

  // Frame the graph whenever a new layout arrives.
  useEffect(() => {
    if (!layout) return
    const id = requestAnimationFrame(() => rf.fitView({ padding: 0.15, duration: 250, maxZoom: 1.2 }))
    return () => cancelAnimationFrame(id)
  }, [layout, rf])

  const onNodeClick: NodeMouseHandler = (_, n) => {
    if (n.type === "symbol") onSelect(n.id)
  }
  const onNodeDoubleClick: NodeMouseHandler = (_, n) => {
    if (n.type === "symbol") onOpen((n as SymbolFlowNode).data.node)
  }

  if (error) {
    return (
      <div className="p-6">
        <Alert variant="destructive">
          <AlertTitle>Layout failed</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      </div>
    )
  }
  if (!layout) {
    return (
      <div className="flex size-full flex-col gap-3 p-8" aria-label="Loading graph" aria-busy>
        <Skeleton className="h-16 w-64" />
        <Skeleton className="h-16 w-80" />
        <Skeleton className="h-16 w-56" />
      </div>
    )
  }
  return (
    <div className="relative size-full" aria-busy={loading || laying}>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        colorMode={theme}
        onNodeClick={onNodeClick}
        onNodeDoubleClick={onNodeDoubleClick}
        onPaneClick={() => onSelect("")}
        nodesConnectable={false}
        deleteKeyCode={null}
        zoomOnDoubleClick={false}
        minZoom={0.05}
        maxZoom={2}
        proOptions={{ hideAttribution: true }}
      >
        <Background gap={22} />
        <Controls showInteractive={false} />
        <MiniMap
          pannable
          zoomable
          className="overflow-hidden rounded-lg border"
          bgColor="var(--card)"
          maskColor="color-mix(in oklab, var(--background) 65%, transparent)"
          nodeColor="var(--muted-foreground)"
        />
      </ReactFlow>
      {(loading || laying) && (
        <div className="pointer-events-none absolute inset-x-0 top-0 h-0.5 animate-pulse bg-primary" />
      )}
    </div>
  )
}

