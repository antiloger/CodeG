import { describe, expect, it } from "vitest"

import { basename, plural, shortKey, shortTypes, timeAgo } from "@/lib/format"

describe("format", () => {
  it("shortens keys and file names", () => {
    expect(shortKey("example.com/m/svc.Run")).toBe("svc.Run")
    expect(shortKey("fmt.Sprint")).toBe("fmt.Sprint")
    expect(basename("internal/query/calls.go")).toBe("calls.go")
    expect(basename("calls.go")).toBe("calls.go")
  })

  it("drops import-path directories from types in a signature", () => {
    expect(shortTypes("func(ix *codeg/internal/query.Index, occs []codeg/internal/query.Occ) map[gk]codeg/internal/model.Key")).toBe(
      "func(ix *query.Index, occs []query.Occ) map[gk]model.Key"
    )
    expect(shortTypes("func(ctx context.Context) (int, error)")).toBe("func(ctx context.Context) (int, error)")
    expect(shortTypes("*golang.org/x/tools/go/packages.Package")).toBe("*packages.Package")
    expect(shortTypes("")).toBe("")
  })

  it("renders relative times", () => {
    const now = Date.parse("2026-01-01T12:00:00Z")
    expect(timeAgo("2026-01-01T11:59:58Z", now)).toBe("just now")
    expect(timeAgo("2026-01-01T11:59:30Z", now)).toBe("30s ago")
    expect(timeAgo("2026-01-01T11:50:00Z", now)).toBe("10m ago")
    expect(timeAgo("2026-01-01T09:00:00Z", now)).toBe("3h ago")
    expect(timeAgo("2025-12-29T12:00:00Z", now)).toBe("3d ago")
    expect(timeAgo("garbage", now)).toBe("")
  })

  it("pluralizes", () => {
    expect(plural(1, "package")).toBe("1 package")
    expect(plural(2, "package")).toBe("2 packages")
    expect(plural(0, "match", "matches")).toBe("0 matches")
  })
})
