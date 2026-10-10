// shortKey drops the import-path directories of a key: example.com/m/svc.Run -> svc.Run.
export function shortKey(key: string): string {
  const i = key.lastIndexOf("/")
  return i >= 0 ? key.slice(i + 1) : key
}

export function basename(path: string): string {
  const i = path.lastIndexOf("/")
  return i >= 0 ? path.slice(i + 1) : path
}

// timeAgo renders an RFC 3339 timestamp relative to now.
export function timeAgo(iso: string, now: number = Date.now()): string {
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return ""
  const s = Math.max(0, Math.round((now - t) / 1000))
  if (s < 5) return "just now"
  if (s < 60) return `${s}s ago`
  const m = Math.round(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.round(m / 60)
  if (h < 48) return `${h}h ago`
  return `${Math.round(h / 24)}d ago`
}

export function plural(n: number, one: string, many = one + "s"): string {
  return `${n} ${n === 1 ? one : many}`
}

// shortTypes drops import-path directories from the types in a signature:
// func(ix *codeg/internal/query.Index) []codeg/internal/query.CallEdge
//   -> func(ix *query.Index) []query.CallEdge
export function shortTypes(detail: string): string {
  return detail.replace(/(?:[\w.~-]+\/)+([A-Za-z_][\w]*\.)/g, "$1")
}
