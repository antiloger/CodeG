import type { ElkNode } from "elkjs/lib/elk-api"

import type { Graph } from "@/api/types"
import { unwrapElk, type ELK } from "@/lib/elk-ctor"
import {
  arrangeFunction,
  buildElk,
  buildPlan,
  placeNodes,
  placePacked,
  usesPacking,
  type BuildOptions,
  type Layout,
} from "@/lib/layout"

let elk: Promise<ELK> | undefined

// ELK's own web-worker protocol: the API class runs on this thread and does
// the work in elk-worker.min.js, so a large layout never blocks the UI.
// (Importing the "bundled" build instead breaks under the production bundler.)
async function getElk(): Promise<ELK> {
  elk ??= (async () => {
    if (typeof Worker === "undefined") {
      // No workers (tests): the bundled build lays out on this thread.
      const bundled = await import("elkjs/lib/elk.bundled.js")
      return new (unwrapElk(bundled.default))()
    }
    const [api, worker] = await Promise.all([
      import("elkjs/lib/elk-api.js"),
      import("elkjs/lib/elk-worker.min.js?url"),
    ])
    return new (unwrapElk(api.default))({ workerUrl: worker.default })
  })()
  return elk
}

export async function runLayout(g: Graph, o: BuildOptions): Promise<Layout> {
  if (g.level === "function") {
    // A function's shape is fixed by roles: arranged directly, without ELK.
    const { plan } = buildPlan(g, o)
    return placePacked(g, arrangeFunction(plan, o.direction), plan, o)
  }
  const elk = await getElk()
  if (usesPacking(g)) {
    const { elk: input, plan } = buildPlan(g, o)
    const laid: ElkNode = await elk.layout(input)
    return placePacked(g, laid, plan, o)
  }
  const laid: ElkNode = await elk.layout(buildElk(g, o))
  return placeNodes(g, laid, o)
}
