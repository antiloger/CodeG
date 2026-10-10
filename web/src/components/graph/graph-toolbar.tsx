import { ArrowDown, ArrowRight, Maximize } from "lucide-react"
import { useReactFlow } from "@xyflow/react"

import { Button } from "@/components/ui/button"
import { Label } from "@/components/ui/label"
import { Separator } from "@/components/ui/separator"
import { Slider } from "@/components/ui/slider"
import { Switch } from "@/components/ui/switch"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { edgeStyle, FILTERABLE } from "@/lib/graph-style"
import type { ViewState } from "@/lib/url-state"

interface Props {
  state: ViewState
  present: string[] // edge kinds that exist in the current graph
  onChange: (patch: Partial<ViewState>) => void
}

// EdgeSwatch draws the line sample of an edge kind: the legend looks like the graph.
export function EdgeSwatch({ kind }: { kind: string }) {
  const st = edgeStyle(kind)
  return (
    <svg width="22" height="8" aria-hidden className="shrink-0">
      <line x1="1" y1="4" x2="21" y2="4" stroke={st.color} strokeWidth="2" strokeDasharray={st.dash} strokeLinecap="round" />
    </svg>
  )
}

function Toggle({ id, label, checked, onChange }: { id: string; label: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <div className="flex items-center gap-2">
      <Switch id={id} size="sm" checked={checked} onCheckedChange={onChange} />
      <Label htmlFor={id} className="text-xs font-normal whitespace-nowrap">
        {label}
      </Label>
    </div>
  )
}

// GraphToolbar holds the view's filters. It must render inside the graph's
// ReactFlowProvider so "fit" can reach the canvas.
export function GraphToolbar({ state, present, onChange }: Props) {
  const rf = useReactFlow()
  const kinds = FILTERABLE[state.view].filter((k) => present.includes(k))
  const visible = kinds.filter((k) => !state.hide.includes(k))

  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b bg-card/60 px-3 py-2" role="toolbar" aria-label="Graph filters">
      <Toggle id="opt-tests" label="Tests" checked={state.tests} onChange={(v) => onChange({ tests: v })} />
      {state.view !== "packages" && (
        <Toggle id="opt-gen" label="Generated" checked={state.generated} onChange={(v) => onChange({ generated: v })} />
      )}
      {state.view === "packages" && (
        <Toggle id="opt-ext" label="Dependencies" checked={state.externals} onChange={(v) => onChange({ externals: v })} />
      )}
      {state.view === "package" && (
        <>
          <Toggle id="opt-methods" label="Methods" checked={state.methods} onChange={(v) => onChange({ methods: v })} />
          <Toggle id="opt-isolated" label="Unconnected" checked={state.isolated} onChange={(v) => onChange({ isolated: v })} />
          <Toggle
            id="opt-imports"
            label="Expand imports"
            checked={state.expand.includes("*")}
            onChange={(v) => onChange({ expand: v ? ["*"] : [] })}
          />
        </>
      )}

      {state.view !== "packages" && (
        <div className="flex items-center gap-2">
          <Label htmlFor="opt-max" className="text-xs font-normal whitespace-nowrap">
            Max nodes
          </Label>
          <Slider
            id="opt-max"
            className="w-32 shrink-0"
            min={50}
            max={2000}
            step={50}
            value={[state.max]}
            onValueChange={(v) => onChange({ max: Array.isArray(v) ? v[0] : v })}
            aria-label="Maximum nodes"
          />
          <span className="w-9 text-xs tabular-nums text-muted-foreground">{state.max}</span>
        </div>
      )}

      <Separator orientation="vertical" className="hidden h-5 sm:block" />

      {kinds.length > 0 && (
        <ToggleGroup
          multiple
          size="sm"
          variant="outline"
          spacing={1}
          className="flex-wrap"
          value={visible}
          onValueChange={(next: string[]) =>
            onChange({ hide: kinds.filter((k) => !next.includes(k)).concat(state.hide.filter((k) => !kinds.includes(k))) })
          }
          aria-label="Edge kinds"
        >
          {kinds.map((k) => (
            <Tooltip key={k}>
              <TooltipTrigger render={<ToggleGroupItem value={k} aria-label={edgeStyle(k).label} className="gap-1.5 px-2 text-xs" />}>
                <EdgeSwatch kind={k} />
                {edgeStyle(k).label}
              </TooltipTrigger>
              <TooltipContent>{visible.includes(k) ? "Hide" : "Show"} {edgeStyle(k).label}</TooltipContent>
            </Tooltip>
          ))}
        </ToggleGroup>
      )}

      <div className="ml-auto flex items-center gap-1">
        <ToggleGroup
          size="sm"
          variant="outline"
          value={[state.dir]}
          onValueChange={(v: string[]) => v[0] && onChange({ dir: v[0] as ViewState["dir"] })}
          aria-label="Layout direction"
        >
          <ToggleGroupItem value="LR" aria-label="Left to right">
            <ArrowRight className="size-3.5" />
          </ToggleGroupItem>
          <ToggleGroupItem value="TB" aria-label="Top to bottom">
            <ArrowDown className="size-3.5" />
          </ToggleGroupItem>
        </ToggleGroup>
        <Tooltip>
          <TooltipTrigger
            render={
              <Button variant="outline" size="icon-sm" aria-label="Fit graph to view" onClick={() => rf.fitView({ padding: 0.15, duration: 250 })} />
            }
          >
            <Maximize />
          </TooltipTrigger>
          <TooltipContent>Fit to view</TooltipContent>
        </Tooltip>
      </div>
    </div>
  )
}
