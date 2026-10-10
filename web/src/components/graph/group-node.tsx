import { memo } from "react"
import type { Node, NodeProps } from "@xyflow/react"

import { cn } from "@/lib/utils"

export type GroupData = { label: string; own: boolean }
export type GroupFlowNode = Node<GroupData, "cluster">

// A package or directory containing nodes.
function GroupNodeImpl({ data }: NodeProps<GroupFlowNode>) {
  return (
    <div
      className={cn(
        "size-full rounded-xl border border-dashed bg-muted/30",
        data.own && "border-primary/60 bg-primary/5"
      )}
    >
      <div
        className={cn(
          "truncate px-3 pt-2 font-mono text-[11px] text-muted-foreground",
          data.own && "font-medium text-foreground"
        )}
      >
        {data.label}
      </div>
    </div>
  )
}

export const GroupNode = memo(GroupNodeImpl)
