import type {
  CallEdge,
  Graph,
  Occurrence,
  SourceView,
  Status,
  SymbolDetail,
  SymbolRef,
} from "./types"

export class ApiError extends Error {
  status: number
  candidates?: SymbolRef[]
  constructor(status: number, message: string, candidates?: SymbolRef[]) {
    super(message)
    this.status = status
    this.candidates = candidates
  }
}

type Params = Record<string, string | number | boolean | undefined>

export function apiURL(path: string, params?: Params): string {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(params ?? {})) {
    if (v === undefined || v === false || v === "") continue
    q.set(k, v === true ? "1" : String(v))
  }
  const s = q.toString()
  return s ? `${path}?${s}` : path
}

export async function getJSON<T>(
  path: string,
  params?: Params,
  signal?: AbortSignal
): Promise<T> {
  let res: Response
  try {
    res = await fetch(apiURL(path, params), { signal })
  } catch (e) {
    if (e instanceof DOMException && e.name === "AbortError") throw e
    throw new ApiError(0, "Cannot reach the codeg server. Is `codeg serve` still running?")
  }
  const body = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new ApiError(res.status, body.error ?? res.statusText, body.candidates)
  }
  return body as T
}

export const api = {
  status: (signal?: AbortSignal) => getJSON<Status>("/api/status", undefined, signal),

  packagesGraph: (p: { tests: boolean; externals: boolean }, signal?: AbortSignal) =>
    getJSON<Graph>("/api/graph/packages", p, signal),

  packageGraph: (
    key: string,
    p: { tests: boolean; generated: boolean; methods: boolean; max: number },
    signal?: AbortSignal
  ) => getJSON<Graph>("/api/graph/package", { key, ...p }, signal),

  functionGraph: (
    key: string,
    p: { tests: boolean; generated: boolean; max: number },
    signal?: AbortSignal
  ) => getJSON<Graph>("/api/graph/function", { key, ...p }, signal),

  symbol: (key: string, signal?: AbortSignal) =>
    getJSON<SymbolDetail>("/api/symbol", { key }, signal),

  source: (
    p: { file: string; start: number; end?: number; context?: number; hl?: string },
    signal?: AbortSignal
  ) => getJSON<SourceView>("/api/source", p, signal),

  references: (
    key: string,
    p: { tests: boolean; generated: boolean },
    signal?: AbortSignal
  ) => getJSON<{ occurrences: Occurrence[] }>("/api/references", { key, ...p }, signal),

  calls: (
    kind: "callers" | "callees",
    key: string,
    p: { tests: boolean; generated: boolean },
    signal?: AbortSignal
  ) => getJSON<{ edges: CallEdge[] }>(`/api/${kind}`, { key, ...p }, signal),

  resolve: (spec: string, signal?: AbortSignal) =>
    getJSON<{ matches: SymbolRef[] }>("/api/resolve", { spec }, signal),
}
