// Mengukur halaman yang melebihi lebar layar di ponsel (390 px): dokumen yang bisa digeser ke samping adalah cacat.
// Pakai (web + API sudah jalan; dari folder web, karena playwright dipasang di sana):
//   SHOT_USER=... SHOT_PASS=... SHOT_WEB=http://localhost:5173 node ../scripts/ui-review/overflow.mjs
// Di Git Bash tambahkan MSYS_NO_PATHCONV=1 bila memberi nilai yang diawali "/" (mis. SHOT_ONLY).
// Env: SHOT_WEB, SHOT_TENANT (DEMO), SHOT_USER, SHOT_PASS, SHOT_PROPERTY (UIREV), SHOT_ONLY (daftar rute, dipisah koma), SHOT_WIDTH (390).
// Keluar dengan kode 1 bila ada halaman yang melebar, jadi bisa dipakai sebagai pemeriksaan.
import fs from 'node:fs'
import path from 'node:path'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'

const { chromium } = createRequire(process.cwd() + '/')('playwright')
const WEB = process.env.SHOT_WEB ?? 'http://localhost:5173'
const TENANT = process.env.SHOT_TENANT ?? 'DEMO'
const CODE = process.env.SHOT_PROPERTY ?? 'UIREV'
const WIDTH = Number(process.env.SHOT_WIDTH ?? 390)
if (!process.env.SHOT_USER || !process.env.SHOT_PASS) { console.error('set SHOT_USER dan SHOT_PASS'); process.exit(2) }

// Rute daftar dari screenshots.mjs (halaman dengan :id dilewati).
const here = path.dirname(fileURLToPath(import.meta.url))
const source = fs.readFileSync(path.join(here, 'screenshots.mjs'), 'utf8')
let routes = [...source.matchAll(/\['(\/[^':]*)', '[^']+'\]/g)].map((m) => m[1]).filter((r) => r !== '/halaman-tidak-ada')
if (process.env.SHOT_ONLY) routes = routes.filter((r) => process.env.SHOT_ONLY.split(',').includes(r))

const browser = await chromium.launch()
const ctx = await browser.newContext({ viewport: { width: WIDTH, height: 844 }, isMobile: true, hasTouch: true, locale: 'id-ID' })
await ctx.addInitScript(() => { try { localStorage.setItem('pms.locale', 'id') } catch { /* tidak apa-apa */ } })
const page = await ctx.newPage()
await page.goto(WEB + '/login')
await page.fill('input[name="tenant_code"]', TENANT)
await page.fill('input[name="email"]', process.env.SHOT_USER)
await page.fill('input[name="password"]', process.env.SHOT_PASS)
await page.click('button[type="submit"]')
await page.waitForURL((u) => !u.pathname.startsWith('/login'), { timeout: 15000 })
await page.waitForLoadState('networkidle').catch(() => {})
// Pilih properti review; daftar propertinya dimuat setelah login, jadi tunggu opsinya.
await page.waitForSelector(`[data-testid="property-switcher"] option:text-matches("${CODE}")`, { state: 'attached', timeout: 10000 }).catch(() => {})
const opt = await page.$(`[data-testid="property-switcher"] option:text-matches("${CODE}")`)
if (opt) await page.selectOption('[data-testid="property-switcher"]', await opt.getAttribute('value'))
else console.log(`properti ${CODE} tidak ditemukan: halaman diukur untuk properti yang terpilih`)
await page.waitForTimeout(1000)

// Elemen paling kanan yang keluar dari layar (untuk menunjuk penyebabnya).
const probe = () => {
  const limit = window.innerWidth
  let worst = null
  for (const el of document.querySelectorAll('body *')) {
    const r = el.getBoundingClientRect()
    if (r.width === 0 || r.right <= limit + 1) continue
    // Isi yang bisa digeser di dalam wadahnya sendiri tidak melebarkan halaman.
    let clipped = false
    for (let p = el.parentElement; p && p !== document.body; p = p.parentElement) {
      const o = getComputedStyle(p).overflowX
      if (o === 'auto' || o === 'scroll' || o === 'hidden') { clipped = true; break }
    }
    if (clipped) continue
    if (!worst || r.right > worst.right) worst = { right: Math.round(r.right), tag: el.tagName.toLowerCase(), cls: String(el.className).slice(0, 70), text: (el.textContent ?? '').trim().slice(0, 40) }
  }
  return { scrollWidth: document.documentElement.scrollWidth, worst }
}

const wide = []
for (const r of routes) {
  try {
    await page.goto(WEB + r)
    await page.waitForLoadState('networkidle').catch(() => {})
    await page.waitForTimeout(600)
    const m = await page.evaluate(probe)
    if (m.scrollWidth > WIDTH) wide.push({ route: r, ...m })
  } catch (e) { console.log('gagal', r, String(e.message).split('\n')[0]) }
}
await browser.close()
if (!wide.length) { console.log(`Tidak ada halaman yang lebih lebar dari ${WIDTH} px (${routes.length} rute).`); process.exit(0) }
console.log(`${wide.length} dari ${routes.length} rute lebih lebar dari ${WIDTH} px:`)
for (const w of wide.sort((a, b) => b.scrollWidth - a.scrollWidth)) console.log(`${String(w.scrollWidth).padStart(5)} px  ${w.route}  <${w.worst?.tag} class="${w.worst?.cls}"> ${w.worst?.text}`)
process.exit(1)
