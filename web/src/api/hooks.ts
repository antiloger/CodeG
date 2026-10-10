import { keepPreviousData, useQuery } from "@tanstack/react-query"

import { ApiError, api } from "@/api/client"
import type { ViewState } from "@/lib/url-state"

// Client errors (unknown key, bad request) will not fix themselves: no retry.
const retry = (count: number, err: Error) =>
  !(err instanceof ApiError && err.status >= 400 && err.status < 500) && count < 2

export function useStatus() {
  return useQuery({
    queryKey: ["status"],
    queryFn: ({ signal }) => api.status(signal),
    // A new generation means `codeg index` committed: the UI refreshes itself.
    refetchInterval: 3000,
    retry: false,
  })
}

export function useGraph(s: ViewState) {
  return useQuery({
    queryKey: ["graph", s.view, s.key, s.tests, s.generated, s.externals, s.methods, s.max],
    queryFn: ({ signal }) => {
      switch (s.view) {
        case "packages":
          return api.packagesGraph({ tests: s.tests, externals: s.externals }, signal)
        case "package":
          return api.packageGraph(
            s.key,
            { tests: s.tests, generated: s.generated, methods: s.methods, max: s.max },
            signal
          )
        case "function":
          return api.functionGraph(s.key, { tests: s.tests, generated: s.generated, max: s.max }, signal)
      }
    },
    placeholderData: keepPreviousData,
    retry,
  })
}

export function useSymbol(key: string) {
  return useQuery({
    queryKey: ["symbol", key],
    queryFn: ({ signal }) => api.symbol(key, signal),
    enabled: key !== "",
    retry,
  })
}

export function useReferences(key: string, tests: boolean, generated: boolean, enabled: boolean) {
  return useQuery({
    queryKey: ["references", key, tests, generated],
    queryFn: ({ signal }) => api.references(key, { tests, generated }, signal),
    enabled: enabled && key !== "",
    retry,
  })
}

export function useCalls(
  kind: "callers" | "callees",
  key: string,
  tests: boolean,
  generated: boolean,
  enabled: boolean
) {
  return useQuery({
    queryKey: ["calls", kind, key, tests, generated],
    queryFn: ({ signal }) => api.calls(kind, key, { tests, generated }, signal),
    enabled: enabled && key !== "",
    retry,
  })
}

export interface SourceWindow {
  file: string
  line: number
  endLine: number
  hl: string
}

export function useSource(w: SourceWindow | undefined) {
  return useQuery({
    queryKey: ["source", w?.file, w?.line, w?.endLine, w?.hl],
    queryFn: ({ signal }) =>
      api.source(
        { file: w!.file, start: w!.line, end: w!.endLine, context: 6, hl: w!.hl },
        signal
      ),
    enabled: w !== undefined,
    retry,
  })
}

export function useResolve(spec: string) {
  return useQuery({
    queryKey: ["resolve", spec],
    queryFn: ({ signal }) => api.resolve(spec, signal),
    enabled: spec.trim().length >= 2,
    retry: false,
  })
}
