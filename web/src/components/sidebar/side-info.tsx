import { ArrowUpRight, MousePointerClick } from "lucide-react"

import type { Graph, GraphNode } from "@/api/types"
import { KindIcon } from "@/components/graph/kind-icon"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { kindStyle } from "@/lib/graph-style"
import { plural } from "@/lib/format"

export function EmptySide({ graph }: { graph?: Graph }) {
  const kinds = new Map<string, number>()
  for (const n of graph?.nodes ?? []) kinds.set(n.kind, (kinds.get(n.kind) ?? 0) + 1)
  return (
    <div className="flex flex-col items-start gap-3 p-6 text-sm">
      <MousePointerClick className="size-5 text-muted-foreground" aria-hidden />
      <p className="font-medium">Nothing selected</p>
      <p className="text-muted-foreground">
        Click a node to inspect it. Double-click to open it: a package opens its symbols, a function opens its graph.
      </p>
      {graph && (
        <p className="text-xs text-muted-foreground">
          {plural(graph.nodes.length, "node")}, {plural(graph.edges.length, "edge")}
          {kinds.size > 0 && ` · ${[...kinds].map(([k, n]) => `${n} ${k}`).join(", ")}`}
        </p>
      )}
    </div>
  )
}

// PackageInfo describes a package or module node (level 0), which has no
// symbol of its own to fetch.
export function PackageInfo({ node, graph, onOpen }: { node: GraphNode; graph: Graph; onOpen: (n: GraphNode) => void }) {
  const imports = graph.edges.filter((e) => e.source === node.id).map((e) => e.target)
  const importedBy = graph.edges.filter((e) => e.target === node.id).map((e) => e.source)
  return (
    <div className="flex flex-col gap-3 p-4">
      <header className="flex flex-col gap-1.5">
        <div className="flex items-center gap-2">
          <KindIcon kind={node.kind} className="size-4 text-muted-foreground" />
          <h2 className="min-w-0 flex-1 truncate font-mono text-sm font-semibold">{node.label}</h2>
        </div>
        <div className="flex flex-wrap gap-1">
          <Badge variant="secondary">{node.kind}</Badge>
          {node.test && <Badge variant="outline">test</Badge>}
          {node.errors && <Badge variant="destructive">type errors</Badge>}
          {node.detail && <Badge variant="outline" className="font-mono">{node.detail}</Badge>}
        </div>
        <p className="break-all font-mono text-[11px] text-muted-foreground">{node.id}</p>
        {node.count ? <p className="text-xs text-muted-foreground">{plural(node.count, "file")}</p> : null}
      </header>
      {node.kind === "package" && (
        <Button size="sm" className="self-start" onClick={() => onOpen(node)}>
          <ArrowUpRight />
          Open package
        </Button>
      )}
      <List title="Imports" ids={imports} />
      <List title="Imported by" ids={importedBy} />
    </div>
  )
}

function List({ title, ids }: { title: string; ids: string[] }) {
  if (ids.length === 0) return null
  return (
    <section>
      <h3 className="mb-1 text-xs font-medium text-muted-foreground">
        {title} <span className="tabular-nums">{ids.length}</span>
      </h3>
      <ul className="flex flex-col">
        {ids.sort().map((id) => (
          <li key={id} className="truncate font-mono text-xs">
            {id}
          </li>
        ))}
      </ul>
    </section>
  )
}

// CollapsedInfo describes a collapsed imported package: what is used from it.
export function CollapsedInfo({
  node,
  onSelect,
  onExpand,
}: {
  node: GraphNode
  onSelect: (key: string) => void
  onExpand: () => void
}) {
  const members = [...(node.members ?? [])].sort((a, b) => a.label.localeCompare(b.label))
  return (
    <div className="flex flex-col gap-3 p-4">
      <header className="flex flex-col gap-1.5">
        <div className="flex items-center gap-2">
          <KindIcon kind="package" className="size-4 text-muted-foreground" />
          <h2 className="min-w-0 flex-1 truncate font-mono text-sm font-semibold">{node.detail}</h2>
        </div>
        <div className="flex flex-wrap gap-1">
          <Badge variant="secondary">imported package</Badge>
          {node.external && <Badge variant="outline">dependency</Badge>}
        </div>
        <p className="text-xs text-muted-foreground">{plural(members.length, "symbol")} used from here.</p>
      </header>
      <Button size="sm" className="self-start" onClick={onExpand}>
        <ArrowUpRight />
        Expand in the graph
      </Button>
      <ul className="flex flex-col" aria-label="Symbols used">
        {members.map((m) => {
          const ks = kindStyle(m.kind)
          return (
            <li key={m.id}>
              <button
                type="button"
                onClick={() => onSelect(m.id)}
                className="flex w-full items-center gap-2 rounded px-2 py-0.5 text-left hover:bg-accent"
              >
                <span className="min-w-0 flex-1 truncate font-mono text-xs">{m.label}</span>
                <Badge variant={ks.variant} className="h-4 px-1.5 text-[10px]">
                  {ks.label}
                </Badge>
              </button>
            </li>
          )
        })}
      </ul>
    </div>
  )
}
