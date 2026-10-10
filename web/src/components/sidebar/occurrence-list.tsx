import type { Occurrence } from "@/api/types"
import { Badge } from "@/components/ui/badge"
import { basename } from "@/lib/format"

const ROLE_VARIANT: Record<string, "default" | "secondary" | "outline" | "destructive"> = {
  definition: "default",
  write: "destructive",
  read_write: "destructive",
  write_partial: "destructive",
  address_taken: "destructive",
  call: "secondary",
}

export function RoleBadge({ role }: { role: string }) {
  return (
    <Badge variant={ROLE_VARIANT[role] ?? "outline"} className="h-4 px-1.5 text-[10px]">
      {role.replace("_", " ")}
    </Badge>
  )
}

export function FlagBadges({ flags }: { flags?: string[] }) {
  return (
    <>
      {flags?.map((f) => (
        <Badge key={f} variant="outline" className="h-4 px-1.5 text-[10px]">
          {f.replace("_", " ")}
        </Badge>
      ))}
    </>
  )
}

interface Props {
  occurrences: Occurrence[]
  onFocus: (o: Occurrence) => void
}

// OccurrenceList shows occurrences grouped by file, each with its source line.
export function OccurrenceList({ occurrences, onFocus }: Props) {
  const byFile = new Map<string, Occurrence[]>()
  for (const o of occurrences) {
    const list = byFile.get(o.file) ?? []
    list.push(o)
    byFile.set(o.file, list)
  }
  return (
    <div className="flex flex-col gap-3">
      {[...byFile].map(([file, list]) => (
        <section key={file} aria-label={file}>
          <h4 className="mb-1 flex items-baseline gap-2 text-xs">
            <span className="min-w-0 truncate font-mono font-medium" title={file}>
              {basename(file)}
            </span>
            <span className="truncate text-muted-foreground">{file.slice(0, file.length - basename(file).length)}</span>
            <span className="ml-auto shrink-0 text-muted-foreground">{list.length}</span>
          </h4>
          <ul className="flex flex-col">
            {list.map((o, i) => (
              <li key={`${o.line}:${o.col}:${i}`}>
                <button
                  type="button"
                  onClick={() => onFocus(o)}
                  className="flex w-full flex-col gap-0.5 rounded-md px-2 py-1 text-left hover:bg-accent focus-visible:bg-accent focus-visible:outline-none"
                >
                  <span className="flex flex-wrap items-center gap-1">
                    <span className="font-mono text-xs tabular-nums text-muted-foreground">
                      {o.line}:{o.col}
                    </span>
                    <RoleBadge role={o.role} />
                    <FlagBadges flags={o.flags} />
                  </span>
                  {o.text && <span className="truncate font-mono text-xs">{o.text}</span>}
                </button>
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  )
}
