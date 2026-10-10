import { useEffect, useRef } from "react"
import { TriangleAlert } from "lucide-react"

import type { SourceView as View } from "@/api/types"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { cn } from "@/lib/utils"

// SourceView is a plain snippet with the lines of interest marked: not a code
// viewer, nothing in it is clickable.
export function SourceSnippet({ view }: { view: View }) {
  const first = useRef<HTMLTableRowElement>(null)
  useEffect(() => {
    first.current?.scrollIntoView({ block: "center" })
  }, [view])
  const firstHighlight = view.lines.findIndex((l) => l.highlight)

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-1.5 text-xs">
        <span className="min-w-0 truncate font-mono text-muted-foreground" title={view.file}>
          {view.file}
        </span>
        <span className="text-muted-foreground">
          :{view.startLine}–{view.endLine}
        </span>
        {view.test && <Badge variant="outline">test</Badge>}
        {view.generated && <Badge variant="outline">generated</Badge>}
      </div>
      {view.stale && (
        <Alert>
          <TriangleAlert />
          <AlertTitle>The file changed after it was indexed</AlertTitle>
          <AlertDescription>Line numbers may be off. Run `codeg index` to refresh.</AlertDescription>
        </Alert>
      )}
      <div className="overflow-auto rounded-lg border bg-muted/30">
        <table className="w-full border-collapse font-mono text-xs leading-5">
          <tbody>
            {view.lines.map((l, i) => (
              <tr
                key={l.n}
                ref={i === firstHighlight ? first : undefined}
                className={cn(l.highlight && "bg-primary/10")}
                aria-current={l.highlight ? "true" : undefined}
              >
                <td
                  className={cn(
                    "sticky left-0 select-none border-r bg-muted/60 px-2 text-right text-muted-foreground tabular-nums",
                    l.highlight && "border-r-2 border-r-primary bg-primary/15 text-foreground"
                  )}
                >
                  {l.n}
                </td>
                <td className="whitespace-pre px-3">{l.text || " "}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {view.truncated && <p className="text-xs text-muted-foreground">Showing the first lines of a long range.</p>}
    </div>
  )
}
