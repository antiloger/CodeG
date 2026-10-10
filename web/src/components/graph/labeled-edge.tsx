import { memo } from "react"
import {
  BaseEdge,
  EdgeLabelRenderer,
  getBezierPath,
  type Edge,
  type EdgeProps,
} from "@xyflow/react"

import { edgeStyle } from "@/lib/graph-style"

export type LabeledData = {
  kind: string
  label: string
  count: number
  certainty?: string
  dim: boolean
}
export type LabeledFlowEdge = Edge<LabeledData, "labeled">

// dash pattern by certainty: a candidate is possible, an unknown cannot be resolved.
function dashFor(kind: string, certainty?: string): string | undefined {
  if (certainty === "unknown") return "2 5"
  if (certainty === "candidate") return "6 4"
  return edgeStyle(kind).dash
}

function LabeledEdgeImpl(props: EdgeProps<LabeledFlowEdge>) {
  const { sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, data, markerEnd } = props
  const [path, labelX, labelY] = getBezierPath({
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
  })
  const kind = data?.kind ?? ""
  const style = edgeStyle(kind)
  const width = style.structural ? 1 : 1 + Math.min(2.5, Math.log2(Math.max(1, data?.count ?? 1)) * 0.6)
  const opacity = data?.dim ? 0.15 : style.structural ? 0.55 : 0.9
  return (
    <>
      <BaseEdge
        path={path}
        markerEnd={markerEnd}
        style={{
          stroke: style.color,
          strokeWidth: width,
          strokeDasharray: dashFor(kind, data?.certainty),
          opacity,
        }}
      />
      {data?.label && !data.dim && (
        <EdgeLabelRenderer>
          <div
            className="nodrag nopan pointer-events-none absolute rounded-full border bg-background/90 px-1.5 py-px font-mono text-[10px] text-foreground shadow-xs"
            style={{ transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)` }}
          >
            {data.label}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  )
}

export const LabeledEdge = memo(LabeledEdgeImpl)
