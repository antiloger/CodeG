import type { ELK, ELKConstructorArguments } from "elkjs/lib/elk-api"

type ELKConstructor = new (args?: ELKConstructorArguments) => ELK

// elkjs is CommonJS: `module.exports = ELK` and `ELK.default = ELK`. Depending
// on the bundler, a default import yields the constructor or the module object
// holding it at `.default`. Node (tests) and the browser bundle disagree, so
// unwrap either.
export function unwrapElk(mod: unknown): ELKConstructor {
  if (typeof mod === "function") return mod as ELKConstructor
  return (mod as { default: ELKConstructor }).default
}

export type { ELK }
