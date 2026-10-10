// The view lives in the URL's query string, never its path: the server serves
// the app for any path without a file extension, and symbol keys contain dots.

export type View = "packages" | "package" | "function"
export type Tab = "overview" | "source" | "references" | "events"
export type Direction = "LR" | "TB"

export interface ViewState {
  view: View
  key: string // package path (package view) or function key (function view)
  pkg: string // the package of a function, for the breadcrumb
  sel: string // selected node id
  tab: Tab
  tests: boolean
  generated: boolean
  externals: boolean
  methods: boolean
  max: number
  hide: string[] // edge kinds hidden by the user
  expand: string[] // imported packages shown symbol by symbol; "*" for all
  isolated: boolean // show nodes that have no visible edge (package views)
  dir: Direction
}

export const DEFAULT_MAX = 250

// Package views are stacks of wide blocks, which read better top to bottom.
export function defaultDir(view: View): Direction {
  return view === "package" ? "TB" : "LR"
}

// Edge kinds hidden until asked for. The noisy relations bury the structure a
// view is for: a package opens on calls and embedding, a function on its
// calls, data and types, without the structural ownership edges (the clusters
// already show that membership).
export function defaultHide(view: View): string[] {
  switch (view) {
    case "package":
      return ["read", "type"]
    case "function":
      return ["contains", "declares", "has_param", "has_result"]
    default:
      return []
  }
}

export const defaults: ViewState = {
  view: "packages",
  key: "",
  pkg: "",
  sel: "",
  tab: "overview",
  tests: false,
  generated: false,
  externals: false,
  methods: false,
  max: DEFAULT_MAX,
  hide: [],
  expand: [],
  isolated: false,
  dir: "LR",
}

const views: View[] = ["packages", "package", "function"]
const tabs: Tab[] = ["overview", "source", "references", "events"]

export function parseSearch(search: string): ViewState {
  const q = new URLSearchParams(search)
  const view = views.find((v) => v === q.get("view")) ?? "packages"
  const key = q.get("key") ?? ""
  // A package or function view without a key is meaningless.
  const valid = view === "packages" || key !== ""
  const max = Number(q.get("max"))
  const v: View = valid ? view : "packages"
  const hideParam = q.get("hide")
  return {
    view: v,
    key: valid ? key : "",
    pkg: q.get("pkg") ?? "",
    sel: q.get("sel") ?? "",
    tab: tabs.find((t) => t === q.get("tab")) ?? "overview",
    tests: q.get("tests") === "1",
    generated: q.get("generated") === "1",
    externals: q.get("externals") === "1",
    methods: q.get("methods") === "1",
    max: Number.isFinite(max) && max >= 20 && max <= 5000 ? Math.round(max) : DEFAULT_MAX,
    // absent: the view's defaults; "-": explicitly nothing hidden
    hide: hideParam === null ? defaultHide(v) : hideParam === "-" ? [] : hideParam.split(",").filter(Boolean),
    expand: (q.get("expand") ?? "").split(",").filter(Boolean),
    isolated: q.get("isolated") === "1",
    dir: q.get("dir") === "TB" || q.get("dir") === "LR" ? (q.get("dir") as Direction) : defaultDir(v),
  }
}

// toSearch omits everything that is at its default, so URLs stay short and
// links stay stable when defaults change.
export function toSearch(s: ViewState): string {
  const q = new URLSearchParams()
  if (s.view !== defaults.view) q.set("view", s.view)
  if (s.key) q.set("key", s.key)
  if (s.pkg) q.set("pkg", s.pkg)
  if (s.sel) q.set("sel", s.sel)
  if (s.tab !== defaults.tab) q.set("tab", s.tab)
  if (s.tests) q.set("tests", "1")
  if (s.generated) q.set("generated", "1")
  if (s.externals) q.set("externals", "1")
  if (s.methods) q.set("methods", "1")
  if (s.max !== defaults.max) q.set("max", String(s.max))
  const hide = [...s.hide].sort().join(",")
  if (hide !== [...defaultHide(s.view)].sort().join(",")) q.set("hide", hide === "" ? "-" : hide)
  if (s.expand.length) q.set("expand", [...s.expand].sort().join(","))
  if (s.isolated) q.set("isolated", "1")
  if (s.dir !== defaultDir(s.view)) q.set("dir", s.dir)
  const out = q.toString()
  return out ? `?${out}` : ""
}

// Moving to another view resets what belongs to the old one.
export function withView(s: ViewState, view: View, key: string, pkg = ""): ViewState {
  return { ...s, view, key, pkg, sel: "", tab: "overview", expand: [], hide: defaultHide(view), dir: defaultDir(view) }
}
