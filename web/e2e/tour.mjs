// End-to-end tour of the UI in a real browser, against a running `codeg serve`.
//
//   codeg index && codeg serve &          # any indexed Go module
//   npx playwright install chromium       # once, unless a browser is already installed
//   CODEG_URL=http://127.0.0.1:7373 npm run e2e
//
// Targets are chosen from the API (the biggest package, its most-calling
// function, one of that function's locals), so it works on any index. Set
// CHROME_PATH to use a specific browser binary and SHOTS=dir to keep
// screenshots. Exits non-zero on the first failed assertion.
import assert from "node:assert/strict"
import { mkdirSync } from "node:fs"
import { chromium } from "playwright"

const base = (process.env.CODEG_URL ?? "http://127.0.0.1:7373").replace(/\/$/, "")
const shots = process.env.SHOTS
if (shots) mkdirSync(shots, { recursive: true })

const api = async (path) => {
  const res = await fetch(base + path)
  assert.equal(res.status, 200, `${path} -> ${res.status}`)
  return res.json()
}

// ---- pick targets from the index ----
const packages = await api("/api/graph/packages")
const pkg = packages.nodes.filter((n) => n.kind === "package" && !n.test).sort((a, b) => (b.count ?? 0) - (a.count ?? 0))[0]
assert.ok(pkg, "the index has no packages")
const pkgGraph = await api(`/api/graph/package?key=${encodeURIComponent(pkg.id)}&methods=1`)
const calls = new Map()
for (const e of pkgGraph.edges) if (e.kind === "call") calls.set(e.source, (calls.get(e.source) ?? 0) + e.count)
const fn = pkgGraph.nodes
  .filter((n) => ["func", "method"].includes(n.kind) && n.group === pkg.id && calls.has(n.id))
  .sort((a, b) => calls.get(b.id) - calls.get(a.id))[0]
assert.ok(fn, `package ${pkg.id} has no function that calls anything`)
const fnGraph = await api(`/api/graph/function?key=${encodeURIComponent(fn.id)}`)
const local = fnGraph.nodes.find((n) => n.kind === "local")
console.log(`targets: package ${pkg.id}, function ${fn.id}, local ${local?.id ?? "(none)"}`)

// ---- drive the browser ----
const browser = await chromium.launch({
  executablePath: process.env.CHROME_PATH || undefined,
  args: ["--no-sandbox"],
})
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
const problems = []
page.on("console", (m) => ["error", "warning"].includes(m.type()) && problems.push(`console.${m.type()}: ${m.text()}`))
page.on("pageerror", (e) => problems.push(`pageerror: ${e.message}`))
page.on("response", (r) => r.status() >= 400 && problems.push(`http ${r.status()}: ${r.url()}`))

const idle = () =>
  page.waitForFunction(() => !document.querySelector(".animate-pulse") && document.querySelectorAll(".react-flow__node").length > 0, null, { timeout: 60000 })
const shot = async (name) => shots && (await page.waitForTimeout(400), page.screenshot({ path: `${shots}/${name}.png` }))
const nodes = () => page.locator(".react-flow__node").count()
const enc = encodeURIComponent
let n = 0
const step = async (name, fn) => {
  process.stdout.write(`${String(++n).padStart(2)}. ${name} ... `)
  await fn()
  console.log("ok")
}

try {
  await step("package graph renders and the page is titled", async () => {
    await page.goto(base + "/")
    await idle()
    assert.equal(await page.title(), "codeg")
    assert.ok((await nodes()) >= packages.nodes.length, "every package should be a node")
    await shot("1-packages")
  })

  await step("a package opens by double-click, in the URL", async () => {
    await page.locator(".react-flow__node-symbol", { hasText: pkg.label }).first().dblclick()
    await page.waitForFunction(() => location.search.includes("view=package"))
    await idle()
    assert.ok(decodeURIComponent(page.url()).includes(`key=${pkg.id}`))
    await shot("2-package")
  })

  await step("selecting a function shows it in the sidebar", async () => {
    await page.goto(`${base}/?view=package&key=${enc(pkg.id)}&methods=1`)
    await idle()
    await page.locator(".react-flow__node-symbol", { hasText: fn.label }).first().click()
    const title = page.locator("[aria-label=Details] h2").first()
    await title.waitFor()
    assert.ok((await title.innerText()).length > 0)
    assert.ok(page.url().includes("sel="))
    await shot("3-sidebar")
  })

  await step("the Source tab highlights the definition", async () => {
    await page.getByRole("tab", { name: "Source" }).click()
    await page.waitForSelector("table")
    assert.equal(await page.locator("table tr[aria-current]").count() >= 1, true)
  })

  await step("References lists occurrences grouped by file", async () => {
    await page.getByRole("tab", { name: "References" }).click()
    await page.waitForFunction(() => document.querySelector("section[aria-label$='.go']") || /No references/.test(document.body.innerText), null, { timeout: 8000 })
  })

  await step("the function graph opens and edges carry source-order numbers", async () => {
    await page.getByRole("button", { name: "Open function graph" }).click()
    await page.waitForFunction(() => location.search.includes("view=function"))
    await idle()
    assert.match(await page.locator("[role=toolbar]").innerText(), /calls/)
    assert.match(await page.locator("body").innerText(), /source order, not execution order/)
    await shot("4-function")
  })

  if (local) {
    await step("a local variable has an ordered Events panel", async () => {
      await page.goto(`${base}/?view=function&key=${enc(fn.id)}&pkg=${enc(pkg.id)}&sel=${enc(local.id)}&tab=events`)
      await idle()
      const events = page.locator("ol[aria-label='Events in source order'] li")
      await events.first().waitFor({ timeout: 8000 })
      assert.ok((await events.count()) >= 1)
      assert.match(await events.first().innerText(), /definition/)
      await shot("5-events")
    })
  }

  await step("search (Ctrl+K) finds the function and opens its graph", async () => {
    await page.goto(base + "/")
    await idle()
    await page.keyboard.press("Control+k")
    await page.getByPlaceholder(/Search:/).fill(fn.id.slice(fn.id.lastIndexOf("/") + 1))
    await page.waitForSelector("[cmdk-item]", { timeout: 8000 })
    await page.keyboard.press("Enter")
    await page.waitForFunction(() => location.search.includes("view=function") || location.search.includes("view=package"))
    await idle()
    await shot("6-search")
  })

  await step("a collapsed imported package expands on double-click", async () => {
    await page.goto(`${base}/?view=package&key=${enc(pkg.id)}`)
    await idle()
    const collapsed = page.locator(".react-flow__node-symbol", { hasText: "used" }).first()
    if ((await collapsed.count()) === 0) return // this package imports nothing that is used
    const before = await nodes()
    await collapsed.dblclick()
    await page.waitForFunction(() => location.search.includes("expand="))
    await idle()
    assert.ok((await nodes()) > before, "expanding should add the package's symbols")
  })

  await step("dark mode toggles with 'd'", async () => {
    const before = await page.evaluate(() => document.documentElement.classList.contains("dark"))
    await page.keyboard.press("d")
    assert.notEqual(await page.evaluate(() => document.documentElement.classList.contains("dark")), before)
    await shot("7-theme")
  })

  await step("Escape clears the selection", async () => {
    await page.goto(`${base}/?view=package&key=${enc(pkg.id)}&sel=${enc(fn.id)}&methods=1`)
    await idle()
    await page.keyboard.press("Escape")
    await page.waitForFunction(() => !location.search.includes("sel="))
  })

  await step("opening a function and pressing Back returns to the package view", async () => {
    await page.goto(`${base}/?view=package&key=${enc(pkg.id)}&methods=1`)
    await idle()
    await page.locator(".react-flow__node-symbol", { hasText: fn.label }).first().dblclick()
    await page.waitForFunction(() => location.search.includes("view=function"))
    await page.goBack()
    await page.waitForFunction(() => location.search.includes("view=package"))
    await idle()
  })

  await step("a bad key shows the API's error instead of crashing", async () => {
    await page.goto(`${base}/?view=function&key=${enc("no/such.Thing")}`)
    await page.waitForSelector("text=Cannot load this view", { timeout: 8000 })
  })

  await step("nothing was logged to the console or failed to load", async () => {
    const real = [...new Set(problems)].filter((p) => !p.includes("no%2Fsuch.Thing") && !p.includes("no/such.Thing") && !p.startsWith("http 404") && !p.includes("404 (Not Found)"))
    assert.deepEqual(real, [])
  })
  console.log("\nall steps passed")
} catch (err) {
  console.log("FAILED")
  console.error(err)
  await shot("failure").catch(() => {})
  process.exitCode = 1
} finally {
  await browser.close()
}
