import { useCallback, useMemo, useSyncExternalStore } from "react"

import { parseSearch, toSearch, type ViewState } from "@/lib/url-state"

const EVENT = "codeg:navigate"

function subscribe(cb: () => void) {
  window.addEventListener("popstate", cb)
  window.addEventListener(EVENT, cb)
  return () => {
    window.removeEventListener("popstate", cb)
    window.removeEventListener(EVENT, cb)
  }
}

const snapshot = () => window.location.search

type Update = ViewState | ((s: ViewState) => ViewState)

// useViewState keeps the whole view in the URL, so any state can be linked,
// reloaded and navigated with the browser's back and forward buttons.
export function useViewState(): [ViewState, (u: Update, opts?: { replace?: boolean }) => void] {
  const search = useSyncExternalStore(subscribe, snapshot)
  const state = useMemo(() => parseSearch(search), [search])

  const set = useCallback((u: Update, opts?: { replace?: boolean }) => {
    const current = parseSearch(window.location.search)
    const next = typeof u === "function" ? u(current) : u
    const url = window.location.pathname + toSearch(next)
    if (url === window.location.pathname + window.location.search) return
    if (opts?.replace) window.history.replaceState(null, "", url)
    else window.history.pushState(null, "", url)
    window.dispatchEvent(new Event(EVENT))
  }, [])

  return [state, set]
}
