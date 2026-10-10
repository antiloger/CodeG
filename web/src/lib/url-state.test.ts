import { describe, expect, it } from "vitest"

import { DEFAULT_MAX, defaultDir, defaultHide, defaults, parseSearch, toSearch, withView } from "@/lib/url-state"

describe("url state", () => {
  it("round-trips a full state and omits defaults", () => {
    expect(toSearch(defaults)).toBe("")
    const s = {
      ...defaults,
      view: "function" as const,
      key: "example.com/m/svc.CreateUser",
      pkg: "example.com/m/svc",
      sel: "example.com/m/svc.CreateUser::u@0",
      tab: "events" as const,
      tests: true,
      generated: true,
      max: 900,
      hide: ["write", "read"],
      isolated: true,
      expand: ["fmt", "example.com/m/lib"],
      dir: "TB" as const,
    }
    const url = toSearch(s)
    expect(url).toContain("view=function")
    expect(url).toContain("hide=read%2Cwrite") // sorted, so links are canonical
    expect(parseSearch(url)).toEqual({ ...s, hide: ["read", "write"], expand: ["example.com/m/lib", "fmt"] })
  })

  it("keeps symbol keys with dots and slashes intact", () => {
    const key = "gopkg.in/yaml.v3.Node.Decode"
    expect(parseSearch(toSearch({ ...defaults, view: "package", key })).key).toBe(key)
  })

  it("falls back on garbage", () => {
    expect(parseSearch("?view=nonsense&tab=zzz&max=abc&dir=x")).toEqual(defaults)
    expect(parseSearch("?view=package")).toEqual(defaults) // a package view needs a key
    expect(parseSearch("?max=5").max).toBe(DEFAULT_MAX)
    expect(parseSearch("?max=99999").max).toBe(DEFAULT_MAX)
  })

  it("hides noisy edge kinds by default, per view, and remembers an explicit choice", () => {
    expect(parseSearch("?view=package&key=a").hide).toEqual(["read", "type"])
    expect(parseSearch("?view=function&key=a.F").hide).toEqual(defaultHide("function"))
    expect(parseSearch("").hide).toEqual([])
    // the defaults are not written to the URL...
    const pkg = { ...defaults, view: "package" as const, key: "a", hide: ["read", "type"], dir: "TB" as const }
    expect(toSearch(pkg)).toBe("?view=package&key=a")
    // ...but showing everything is, so it survives a reload
    const all = { ...pkg, hide: [] }
    expect(toSearch(all)).toContain("hide=-")
    expect(parseSearch(toSearch(all)).hide).toEqual([])
    const some = { ...pkg, hide: ["write"] }
    expect(parseSearch(toSearch(some)).hide).toEqual(["write"])
  })

  it("flows package views top to bottom by default, and remembers an explicit choice", () => {
    expect(parseSearch("?view=package&key=a").dir).toBe("TB")
    expect(parseSearch("?view=function&key=a.F").dir).toBe("LR")
    expect(parseSearch("").dir).toBe("LR")
    const lr = { ...defaults, view: "package" as const, key: "a", hide: defaultHide("package"), dir: "LR" as const }
    expect(toSearch(lr)).toContain("dir=LR")
    expect(parseSearch(toSearch(lr)).dir).toBe("LR")
    expect(toSearch({ ...lr, dir: defaultDir("package") })).toBe("?view=package&key=a")
  })

  it("resets selection and tab when moving between views", () => {
    const s = { ...defaults, view: "package" as const, key: "a", sel: "a.F", tab: "source" as const, tests: true, expand: ["fmt"] }
    const next = withView(s, "function", "a.F", "a")
    expect(next).toMatchObject({ view: "function", key: "a.F", pkg: "a", sel: "", tab: "overview", tests: true, expand: [], hide: defaultHide("function"), dir: "LR" })
  })
})
