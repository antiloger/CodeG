import { useState } from "react"

import { useResolve } from "@/api/hooks"
import type { SymbolRef } from "@/api/types"
import { KindIcon } from "@/components/graph/kind-icon"
import { Badge } from "@/components/ui/badge"
import {
  Command,
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command"
import { useDebounced } from "@/hooks/use-debounced"
import { kindStyle } from "@/lib/graph-style"

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  onPick: (s: SymbolRef) => void
}

// SearchDialog finds symbols by name. Matching is done by the server (the same
// resolver as `codeg query`): a full key, pkg.Name, Type.Method, Func::local.
export function SearchDialog({ open, onOpenChange, onPick }: Props) {
  const [text, setText] = useState("")
  const spec = useDebounced(text.trim(), 200)
  const q = useResolve(spec)
  const matches = q.data?.matches ?? []
  const searching = text.trim().length >= 2 && (spec !== text.trim() || q.isFetching)

  return (
    <CommandDialog open={open} onOpenChange={onOpenChange} title="Search symbols" description="Find a package, type, function or variable by name">
      {/* The server does the matching; cmdk must not filter again. */}
      <Command shouldFilter={false}>
        <CommandInput
          placeholder="Search: Add, lib.Add, Store.Save, Func::x, file.go:12"
          value={text}
          onValueChange={setText}
        />
        <CommandList>
          {text.trim().length < 2 && <div className="py-6 text-center text-sm text-muted-foreground">Type at least two characters.</div>}
          {text.trim().length >= 2 && !searching && matches.length === 0 && (
            <CommandEmpty>{q.error ? "No symbol matches." : "No results."}</CommandEmpty>
          )}
          {matches.length > 0 && (
            <CommandGroup heading={`${matches.length} ${matches.length === 1 ? "match" : "matches"}`}>
              {matches.slice(0, 60).map((m) => {
                const ks = kindStyle(m.kind)
                return (
                  <CommandItem
                    key={m.key}
                    value={m.key}
                    onSelect={() => {
                      onPick(m)
                      onOpenChange(false)
                      setText("")
                    }}
                  >
                    <KindIcon kind={m.kind} className="size-4 text-muted-foreground" />
                    <span className="min-w-0 flex-1 truncate font-mono text-xs">{m.key}</span>
                    <Badge variant={ks.variant}>{ks.label}</Badge>
                  </CommandItem>
                )
              })}
            </CommandGroup>
          )}
        </CommandList>
      </Command>
    </CommandDialog>
  )
}
