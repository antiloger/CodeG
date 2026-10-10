import { ArrowRight } from "lucide-react"

import type { VariableEvent } from "@/api/types"
import { FlagBadges, RoleBadge } from "@/components/sidebar/occurrence-list"
import { Button } from "@/components/ui/button"
import { shortKey } from "@/lib/format"

interface Props {
  events: VariableEvent[]
  onFocus: (e: VariableEvent) => void
  onSelect: (key: string) => void
}

// EventsList is the variable panel: everything that happens to a variable, in
// source order, each with the one-hop links the index records:
// `x -> f(x)` (which call an argument goes to) and `y := x` (where it flows).
export function EventsList({ events, onFocus, onSelect }: Props) {
  return (
    <div className="flex flex-col gap-2">
      <p className="text-xs text-muted-foreground">Source order, not execution order.</p>
      <ol className="flex flex-col gap-1" aria-label="Events in source order">
        {events.map((e, i) => (
          <li key={`${e.line}:${e.col}:${e.role}:${i}`} className="rounded-md border bg-card">
            <button
              type="button"
              onClick={() => onFocus(e)}
              className="flex w-full flex-col gap-1 rounded-md px-2 py-1.5 text-left hover:bg-accent focus-visible:bg-accent focus-visible:outline-none"
            >
              <span className="flex flex-wrap items-center gap-1">
                <span className="w-5 text-right font-mono text-[10px] tabular-nums text-muted-foreground">{i + 1}</span>
                <span className="font-mono text-xs tabular-nums text-muted-foreground">
                  {e.file.split("/").pop()}:{e.line}
                </span>
                <RoleBadge role={e.role} />
                <FlagBadges flags={e.flags} />
              </span>
              {e.text && <span className="truncate pl-6 font-mono text-xs">{e.text}</span>}
            </button>
            {(e.passedTo || e.flowTo) && (
              <div className="flex flex-wrap items-center gap-1 border-t px-2 py-1 pl-8 text-xs">
                {e.passedTo && (
                  <Button variant="ghost" size="xs" onClick={() => onSelect(e.passedTo!.key)}>
                    <ArrowRight />
                    argument {e.argIndex ?? 0} of <span className="font-mono">{shortKey(e.passedTo.key)}</span>
                  </Button>
                )}
                {e.flowTo && (
                  <Button variant="ghost" size="xs" onClick={() => onSelect(e.flowTo!)}>
                    <ArrowRight />
                    flows into <span className="font-mono">{shortKey(e.flowTo)}</span>
                  </Button>
                )}
              </div>
            )}
          </li>
        ))}
      </ol>
    </div>
  )
}
