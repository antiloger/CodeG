import { useState } from "react"
import { ArrowUpRight, Info } from "lucide-react"

import { useReferences, useSource, useSymbol } from "@/api/hooks"
import type { Occurrence, Relation, SymbolDetail } from "@/api/types"
import { KindIcon } from "@/components/graph/kind-icon"
import { CallList } from "@/components/sidebar/call-list"
import { EventsList } from "@/components/sidebar/events-list"
import { OccurrenceList } from "@/components/sidebar/occurrence-list"
import { SourceSnippet } from "@/components/sidebar/source-view"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Skeleton } from "@/components/ui/skeleton"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { shortKey, shortTypes } from "@/lib/format"
import { kindStyle } from "@/lib/graph-style"
import type { Tab, ViewState } from "@/lib/url-state"

interface Props {
  symbol: string
  state: ViewState
  onTab: (t: Tab) => void
  onSelect: (key: string) => void
  onOpenFunction: (key: string, pkg: string) => void
}

const FUNCTION_KINDS = new Set(["func", "method", "func_lit"])

function Header({ d }: { d: SymbolDetail }) {
  const s = d.symbol
  const ks = kindStyle(s.kind)
  return (
    <header className="flex flex-col gap-1.5">
      <div className="flex items-center gap-2">
        <KindIcon kind={s.kind} className="size-4 shrink-0 text-muted-foreground" />
        <h2 className="min-w-0 flex-1 truncate font-mono text-sm font-semibold" title={s.name}>
          {s.name || shortKey(s.key)}
        </h2>
      </div>
      <div className="flex flex-wrap items-center gap-1">
        <Badge variant={ks.variant}>{ks.label}</Badge>
        {s.external && <Badge variant="outline">dependency</Badge>}
        {s.location?.test && <Badge variant="outline">test</Badge>}
        {s.location?.generated && <Badge variant="outline">generated</Badge>}
        {s.module && (
          <Badge variant="outline" className="font-mono">
            {s.module}
            {s.version ? `@${s.version}` : ""}
          </Badge>
        )}
      </div>
      <p className="break-all font-mono text-[11px] text-muted-foreground">{s.key}</p>
      {s.detail && <pre className="overflow-x-auto whitespace-pre-wrap break-words rounded-md bg-muted/50 p-2 font-mono text-xs">{shortTypes(s.detail)}</pre>}
      {s.location && (
        <p className="font-mono text-xs text-muted-foreground">
          {s.location.file}:{s.location.line}
        </p>
      )}
    </header>
  )
}

function Stats({ stats }: { stats: Record<string, number> }) {
  const entries = Object.entries(stats).sort(([a], [b]) => a.localeCompare(b))
  if (entries.length === 0) return null
  return (
    <section aria-label="Occurrences by role">
      <h3 className="mb-1 text-xs font-medium text-muted-foreground">Occurrences</h3>
      <div className="flex flex-wrap gap-1">
        {entries.map(([role, n]) => (
          <Badge key={role} variant="secondary">
            {role.replace("_", " ")} <span className="ml-1 tabular-nums">{n}</span>
          </Badge>
        ))}
      </div>
    </section>
  )
}

const RELATION_TITLES: Record<string, string> = {
  has_field: "Fields",
  has_method: "Methods",
  embeds: "Embeds",
  has_param: "Parameters",
  has_result: "Results",
  contains: "Contains",
  type_param: "Type parameters",
  alias_of: "Alias of",
}

function Relations({ relations, onSelect }: { relations: Relation[]; onSelect: (k: string) => void }) {
  const groups = new Map<string, Relation[]>()
  for (const r of relations) {
    const g = groups.get(r.kind) ?? []
    g.push(r)
    groups.set(r.kind, g)
  }
  return (
    <>
      {[...groups].map(([kind, list]) => (
        <Collapsible key={kind} defaultOpen={list.length <= 12}>
          <CollapsibleTrigger className="flex w-full items-center justify-between rounded-md px-1 py-1 text-xs font-medium text-muted-foreground hover:bg-accent">
            {RELATION_TITLES[kind] ?? kind}
            <span className="tabular-nums">{list.length}</span>
          </CollapsibleTrigger>
          <CollapsibleContent>
            <ul className="flex flex-col">
              {list.map((r) => (
                <li key={r.symbol.key}>
                  <button
                    type="button"
                    onClick={() => onSelect(r.symbol.key)}
                    className="flex w-full items-baseline gap-2 rounded px-2 py-0.5 text-left hover:bg-accent"
                  >
                    <span className="font-mono text-xs">{r.symbol.name || shortKey(r.symbol.key)}</span>
                    <span className="min-w-0 flex-1 truncate font-mono text-[11px] text-muted-foreground">{r.symbol.detail ? shortTypes(r.symbol.detail) : ""}</span>
                  </button>
                </li>
              ))}
            </ul>
          </CollapsibleContent>
        </Collapsible>
      ))}
    </>
  )
}

function Overview({ d, state, onSelect, onFocus }: { d: SymbolDetail; state: ViewState; onSelect: (k: string) => void; onFocus: (o: Occurrence) => void }) {
  const [calls, setCalls] = useState<"callers" | "callees" | undefined>()
  const impl = d.implements
  return (
    <div className="flex flex-col gap-4">
      <Stats stats={d.stats} />

      {(d.callers !== undefined || d.callees !== undefined) && (
        <section aria-label="Calls" className="flex flex-col gap-2">
          <div className="flex flex-wrap gap-2">
            <Button variant={calls === "callers" ? "default" : "outline"} size="sm" onClick={() => setCalls(calls === "callers" ? undefined : "callers")}>
              Callers <span className="tabular-nums">{d.callers}</span>
            </Button>
            <Button variant={calls === "callees" ? "default" : "outline"} size="sm" onClick={() => setCalls(calls === "callees" ? undefined : "callees")}>
              Callees <span className="tabular-nums">{d.callees}</span>
            </Button>
          </div>
          {calls && (
            <CallList kind={calls} symbol={d.symbol.key} tests={state.tests} generated={state.generated} onSelect={onSelect} onFocus={onFocus} />
          )}
        </section>
      )}

      {impl && (
        <section aria-label="Implements" className="flex flex-col gap-1">
          <h3 className="text-xs font-medium text-muted-foreground">
            {impl.isInterface ? `Implemented by ${impl.implementers?.length ?? 0}` : `Implements ${impl.implemented?.length ?? 0}`}
          </h3>
          <ul className="flex flex-col">
            {(impl.implementers ?? []).map((i) => (
              <li key={i.type.key}>
                <button type="button" onClick={() => onSelect(i.type.key)} className="flex flex-wrap items-center gap-1 rounded px-2 py-0.5 text-left hover:bg-accent">
                  <span className="font-mono text-xs">{shortKey(i.type.key)}</span>
                  {i.viaPointer && <Badge variant="outline" className="h-4 px-1.5 text-[10px]">*T only</Badge>}
                  {i.viaEmbedding && <Badge variant="outline" className="h-4 px-1.5 text-[10px]">embedding</Badge>}
                  {i.interface && <Badge variant="outline" className="h-4 px-1.5 text-[10px]">interface</Badge>}
                </button>
              </li>
            ))}
            {(impl.implemented ?? []).map((i) => (
              <li key={i.interface.key}>
                <button type="button" onClick={() => onSelect(i.interface.key)} className="flex flex-wrap items-center gap-1 rounded px-2 py-0.5 text-left hover:bg-accent">
                  <span className="font-mono text-xs">{shortKey(i.interface.key)}</span>
                  {i.viaPointer && <Badge variant="outline" className="h-4 px-1.5 text-[10px]">*T only</Badge>}
                </button>
              </li>
            ))}
          </ul>
          <p className="text-[11px] text-muted-foreground">{impl.limits}</p>
        </section>
      )}

      {d.relations && d.relations.length > 0 && (
        <section aria-label="Relations">
          <Relations relations={d.relations} onSelect={onSelect} />
        </section>
      )}

      {d.limits && d.limits.length > 0 && (
        <Alert>
          <Info />
          <AlertTitle>What static analysis cannot tell</AlertTitle>
          <AlertDescription>
            <ul className="list-disc pl-4">
              {d.limits.map((l) => (
                <li key={l}>{l}</li>
              ))}
            </ul>
          </AlertDescription>
        </Alert>
      )}
    </div>
  )
}

function FocusedSource({ focus, fallback }: { focus?: Occurrence; fallback: SymbolDetail["source"] }) {
  const window = focus
    ? {
        file: focus.file,
        line: Math.max(1, focus.line - 6),
        endLine: focus.endLine + 6,
        hl: `${focus.line}:${focus.col}-${focus.endLine}:${focus.endCol}`,
      }
    : undefined
  const q = useSource(window)
  if (focus) {
    if (q.isPending) return <Skeleton className="h-40 w-full" />
    if (q.error) return <Alert variant="destructive"><AlertDescription>{q.error.message}</AlertDescription></Alert>
    return <SourceSnippet view={q.data} />
  }
  if (!fallback) return <p className="text-xs text-muted-foreground">No source for this symbol: it belongs to a dependency.</p>
  return <SourceSnippet view={fallback} />
}

function References({ symbol, state, onFocus }: { symbol: string; state: ViewState; onFocus: (o: Occurrence) => void }) {
  const q = useReferences(symbol, state.tests, state.generated, true)
  if (q.isPending) return <Skeleton className="h-24 w-full" />
  if (q.error) return <Alert variant="destructive"><AlertDescription>{q.error.message}</AlertDescription></Alert>
  if (q.data.occurrences.length === 0) return <p className="text-xs text-muted-foreground">No references.</p>
  return (
    <div className="flex flex-col gap-2">
      <p className="text-xs text-muted-foreground">
        {q.data.occurrences.length} references
        {!state.tests && " (test files hidden)"}
      </p>
      <OccurrenceList occurrences={q.data.occurrences} onFocus={onFocus} />
    </div>
  )
}

// SymbolPanel is the sidebar for one symbol. It is keyed by the symbol, so its
// local state (the focused occurrence) resets when the selection changes.
export function SymbolPanel({ symbol, state, onTab, onSelect, onOpenFunction }: Props) {
  const q = useSymbol(symbol)
  const [focus, setFocus] = useState<Occurrence>()

  if (q.isPending) {
    return (
      <div className="flex flex-col gap-3 p-4" aria-busy aria-label="Loading symbol">
        <Skeleton className="h-6 w-2/3" />
        <Skeleton className="h-20 w-full" />
        <Skeleton className="h-32 w-full" />
      </div>
    )
  }
  if (q.error) {
    return (
      <div className="p-4">
        <Alert variant="destructive">
          <AlertTitle>Cannot show this symbol</AlertTitle>
          <AlertDescription>{q.error.message}</AlertDescription>
        </Alert>
      </div>
    )
  }
  const d = q.data
  const hasEvents = !!d.events && d.events.length > 0
  const tab: Tab = state.tab === "events" && !hasEvents ? "overview" : state.tab
  const focusOn = (o: Occurrence) => {
    setFocus(o)
    onTab("source")
  }

  return (
    <div className="flex flex-col gap-3 p-4">
      <Header d={d} />
      {FUNCTION_KINDS.has(d.symbol.kind) && !d.symbol.external && (
        <Button variant="outline" size="sm" className="self-start" onClick={() => onOpenFunction(d.symbol.key, d.symbol.package)}>
          <ArrowUpRight />
          Open function graph
        </Button>
      )}
      <Tabs value={tab} onValueChange={(v) => onTab(v as Tab)}>
        <TabsList className="w-full">
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="source">Source</TabsTrigger>
          <TabsTrigger value="references">References</TabsTrigger>
          {hasEvents && <TabsTrigger value="events">Events</TabsTrigger>}
        </TabsList>
        <TabsContent value="overview" className="pt-3">
          <Overview d={d} state={state} onSelect={onSelect} onFocus={focusOn} />
        </TabsContent>
        <TabsContent value="source" className="pt-3">
          <FocusedSource focus={focus} fallback={d.source} />
          {focus && (
            <Button variant="ghost" size="sm" className="mt-2" onClick={() => setFocus(undefined)}>
              Back to the definition
            </Button>
          )}
        </TabsContent>
        <TabsContent value="references" className="pt-3">
          <References symbol={d.symbol.key} state={state} onFocus={focusOn} />
        </TabsContent>
        {hasEvents && (
          <TabsContent value="events" className="pt-3">
            <EventsList events={d.events!} onFocus={focusOn} onSelect={onSelect} />
          </TabsContent>
        )}
      </Tabs>
    </div>
  )
}
