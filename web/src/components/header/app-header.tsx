import { Fragment } from "react"
import { Monitor, Moon, Search, Sun, TriangleAlert } from "lucide-react"

import type { Status } from "@/api/types"
import { useTheme } from "@/components/theme-provider"
import { Badge } from "@/components/ui/badge"
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Kbd } from "@/components/ui/kbd"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { plural, timeAgo } from "@/lib/format"
import type { Crumb } from "@/lib/navigate"
import type { ViewState } from "@/lib/url-state"

interface Props {
  crumbs: Crumb[]
  status?: Status
  statusError?: string
  onNavigate: (to: ViewState) => void
  onSearch: () => void
}

function ThemeMenu() {
  const { theme, setTheme } = useTheme()
  const Icon = theme === "dark" ? Moon : theme === "light" ? Sun : Monitor
  return (
    <DropdownMenu>
      <Tooltip>
        <TooltipTrigger render={<DropdownMenuTrigger render={<Button variant="ghost" size="icon" aria-label="Theme" />} />}>
          <Icon />
        </TooltipTrigger>
        <TooltipContent>
          Theme <Kbd>d</Kbd>
        </TooltipContent>
      </Tooltip>
      <DropdownMenuContent align="end">
        <DropdownMenuRadioGroup value={theme} onValueChange={(v) => setTheme(v as typeof theme)}>
          <DropdownMenuRadioItem value="light">Light</DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="dark">Dark</DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="system">System</DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

export function AppHeader({ crumbs, status, statusError, onNavigate, onSearch }: Props) {
  return (
    <header className="flex h-12 shrink-0 items-center gap-3 border-b bg-card px-3">
      <span className="font-mono text-sm font-semibold tracking-tight">codeg</span>
      <Breadcrumb className="min-w-0 flex-1">
        <BreadcrumbList className="flex-nowrap">
          {crumbs.map((c, i) => (
            <Fragment key={i}>
              {i > 0 && <BreadcrumbSeparator />}
              <BreadcrumbItem className="min-w-0">
                {c.to ? (
                  <BreadcrumbLink
                    render={<button type="button" onClick={() => onNavigate(c.to!)} />}
                    className="max-w-64 truncate font-mono text-xs"
                  >
                    {c.label}
                  </BreadcrumbLink>
                ) : (
                  <BreadcrumbPage className="max-w-96 truncate font-mono text-xs">{c.label}</BreadcrumbPage>
                )}
              </BreadcrumbItem>
            </Fragment>
          ))}
        </BreadcrumbList>
      </Breadcrumb>

      <Button variant="outline" size="sm" onClick={onSearch} className="gap-2 text-muted-foreground">
        <Search />
        <span className="hidden sm:inline">Search symbols</span>
        <Kbd>⌘K</Kbd>
      </Button>

      {statusError ? (
        <Badge variant="destructive">
          <TriangleAlert /> server unreachable
        </Badge>
      ) : status ? (
        <Tooltip>
          <TooltipTrigger render={<Badge variant="secondary" className="hidden font-normal md:inline-flex" />}>
            {plural(status.packages, "package")} · indexed {timeAgo(status.indexedAt)}
          </TooltipTrigger>
          <TooltipContent className="max-w-xs">
            <div className="font-mono">{status.root}</div>
            <div>
              {status.config.GoVersion} {status.config.GOOS}/{status.config.GOARCH}
              {status.config.Tags?.length ? ` · tags: ${status.config.Tags.join(",")}` : ""}
            </div>
            <div>
              {plural(status.files, "file")}
              {status.excluded > 0 && `, ${status.excluded} excluded by build constraints`}
              {status.withErrors > 0 && `, ${plural(status.withErrors, "package")} with type errors`}
            </div>
          </TooltipContent>
        </Tooltip>
      ) : null}
      <ThemeMenu />
    </header>
  )
}
