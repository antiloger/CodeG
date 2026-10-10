import { useCalls } from "@/api/hooks"
import type { CallEdge, Occurrence } from "@/api/types"
import { FlagBadges } from "@/components/sidebar/occurrence-list"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Skeleton } from "@/components/ui/skeleton"
import { shortKey } from "@/lib/format"

interface Props {
  kind: "callers" | "callees"
  symbol: string
  tests: boolean
  generated: boolean
  onSelect: (key: string) => void
  onFocus: (o: Occurrence) => void
}

const CERTAINTY_NOTE: Record<string, string> = {
  candidate: "candidate: one of several possible targets",
  unknown: "unknown target",
}

function Edge({ e, kind, onSelect, onFocus }: { e: CallEdge; kind: Props["kind"]; onSelect: Props["onSelect"]; onFocus: Props["onFocus"] }) {
  const other = kind === "callers" ? e.caller : e.callee
  return (
    <li className="rounded-md border bg-card p-2">
      <div className="flex flex-wrap items-center gap-1">
        {other ? (
          <button
            type="button"
            className="min-w-0 truncate font-mono text-xs font-medium hover:underline"
            onClick={() => onSelect(other.key)}
          >
            {shortKey(other.key)}
          </button>
        ) : (
          <span className="text-xs font-medium">computed call</span>
        )}
        <Badge variant="outline" className="h-4 px-1.5 text-[10px]">
          {e.dispatch}
        </Badge>
        {e.certainty !== "certain" && (
          <Badge variant="secondary" className="h-4 px-1.5 text-[10px]">
            {e.certainty}
          </Badge>
        )}
        <span className="ml-auto text-xs text-muted-foreground">{e.sites.length}×</span>
      </div>
      {(e.note || CERTAINTY_NOTE[e.certainty]) && (
        <p className="mt-1 text-xs text-muted-foreground">{e.note ?? CERTAINTY_NOTE[e.certainty]}</p>
      )}
      <ul className="mt-1 flex flex-col">
        {e.sites.map((o, i) => (
          <li key={`${o.line}:${o.col}:${i}`}>
            <button
              type="button"
              onClick={() => onFocus(o)}
              className="flex w-full flex-col rounded px-1 py-0.5 text-left hover:bg-accent"
            >
              <span className="flex flex-wrap items-center gap-1">
                <span className="font-mono text-[11px] tabular-nums text-muted-foreground">
                  {o.file.split("/").pop()}:{o.line}
                </span>
                <FlagBadges flags={o.flags} />
              </span>
              {o.text && <span className="truncate font-mono text-xs">{o.text}</span>}
            </button>
          </li>
        ))}
      </ul>
      {e.candidates && e.candidates.length > 0 && (
        <div className="mt-1 border-t pt-1 text-xs">
          <span className="text-muted-foreground">possible concrete targets:</span>
          <div className="mt-0.5 flex flex-wrap gap-1">
            {e.candidates.map((c) => (
              <button
                key={c.key}
                type="button"
                onClick={() => onSelect(c.key)}
                className="rounded border border-dashed px-1.5 py-px font-mono text-[11px] hover:bg-accent"
              >
                {shortKey(c.key)}
              </button>
            ))}
          </div>
        </div>
      )}
    </li>
  )
}

export function CallList({ kind, symbol, tests, generated, onSelect, onFocus }: Props) {
  const q = useCalls(kind, symbol, tests, generated, true)
  if (q.isPending) return <Skeleton className="h-16 w-full" />
  if (q.error) {
    return (
      <Alert variant="destructive">
        <AlertDescription>{q.error.message}</AlertDescription>
      </Alert>
    )
  }
  if (q.data.edges.length === 0) return <p className="text-xs text-muted-foreground">None found.</p>
  return (
    <ul className="flex flex-col gap-2" aria-label={kind}>
      {q.data.edges.map((e, i) => (
        <Edge key={i} e={e} kind={kind} onSelect={onSelect} onFocus={onFocus} />
      ))}
    </ul>
  )
}
