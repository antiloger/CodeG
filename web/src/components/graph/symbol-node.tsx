import { memo } from "react"
import { Handle, Position, type Node, type NodeProps } from "@xyflow/react"
import { TriangleAlert } from "lucide-react"

import type { GraphNode } from "@/api/types"
import { KindIcon } from "@/components/graph/kind-icon"
import { Badge } from "@/components/ui/badge"
import { shortTypes } from "@/lib/format"
import { kindStyle } from "@/lib/graph-style"
import { cn } from "@/lib/utils"
import type { Direction } from "@/lib/url-state"

export type SymbolData = { node: GraphNode; direction: Direction; dim: boolean }
export type SymbolFlowNode = Node<SymbolData, "symbol">

// A symbol or package. Everything visual comes from theme tokens.
function SymbolNodeImpl({ data, selected }: NodeProps<SymbolFlowNode>) {
  const { node, direction, dim } = data
  const ks = kindStyle(node.kind)
  const [target, source] =
    direction === "LR" ? [Position.Left, Position.Right] : [Position.Top, Position.Bottom]
  const central = node.kind === "function" || node.kind === "closure"
  return (
    <div
      className={cn(
        "size-full rounded-lg border bg-card px-2.5 py-1.5 text-card-foreground shadow-xs transition-opacity",
        node.external && "border-dashed",
        central && "border-primary bg-primary/5",
        selected && "ring-2 ring-primary",
        dim && "opacity-40"
      )}
      title={node.detail ? `${node.label}\n${node.detail}` : node.label}
    >
      <Handle type="target" position={target} className="!size-1.5 !border-0 !bg-muted-foreground/60" />
      <div className="flex items-center gap-1.5">
        <KindIcon kind={node.kind} className="size-3.5 shrink-0 text-muted-foreground" />
        <span
          className={cn(
            "min-w-0 flex-1 truncate font-mono text-xs",
            node.exported === false && "text-muted-foreground",
            central && "font-semibold"
          )}
        >
          {node.label}
        </span>
        {node.errors && <TriangleAlert className="size-3.5 shrink-0 text-destructive" aria-label="has type errors" />}
      </div>
      <div className="mt-1 flex items-center gap-1">
        <Badge variant={ks.variant} className="h-4 px-1.5 text-[10px]">
          {ks.label}
        </Badge>
        {node.test && (
          <Badge variant="outline" className="h-4 px-1.5 text-[10px]">
            test
          </Badge>
        )}
        {node.generated && (
          <Badge variant="outline" className="h-4 px-1.5 text-[10px]">
            generated
          </Badge>
        )}
        {node.external && node.kind !== "module" && (
          <Badge variant="outline" className="h-4 px-1.5 text-[10px]">
            dep
          </Badge>
        )}
        {node.members ? (
          <span className="ml-auto text-[10px] text-muted-foreground">{node.count} used</span>
        ) : node.kind === "package" && node.count ? (
          <span className="ml-auto text-[10px] text-muted-foreground">{node.count} files</span>
        ) : node.count ? (
          <span className="ml-auto text-[10px] text-muted-foreground">{node.count} members</span>
        ) : null}
      </div>
      {node.detail && node.kind !== "package" && node.kind !== "module" && (
        <div className="mt-0.5 truncate font-mono text-[10px] text-muted-foreground">{shortTypes(node.detail)}</div>
      )}
      <Handle type="source" position={source} className="!size-1.5 !border-0 !bg-muted-foreground/60" />
    </div>
  )
}

export const SymbolNode = memo(SymbolNodeImpl)
