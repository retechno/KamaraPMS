// Memotret semua halaman KamaraPMS untuk review UI.
// Pakai: (aplikasi web + API sudah jalan)
//   cd web && npm i -D playwright && npx playwright install chromium
//   SHOT_USER=... SHOT_PASS=... node ../scripts/ui-review/screenshots.mjs
// Env: SHOT_WEB (http://localhost:5173), PMS_API (http://127.0.0.1:18080), SHOT_TENANT (DEMO),
//      SHOT_PROPERTY (UIREV), SHOT_OUT (screenshots), SHOT_LABEL (admin) untuk membedakan putaran per peran,
//      SHOT_ONLY (opsional, mis. "/,/room-status") untuk memotret sebagian saja.
import { createRequire } from 'node:module'
// resolusi dari folder kerja (web/), karena skrip ini tinggal di luar web/
const { chromium } = createRequire(process.cwd() + '/')('playwright')
import fs from 'node:fs'
import path from 'node:path'

const WEB = process.env.SHOT_WEB ?? 'http://localhost:5173'
const API = (process.env.PMS_API ?? 'http://127.0.0.1:18080') + '/api/v1'
const TENANT = process.env.SHOT_TENANT ?? 'DEMO'
const USER = process.env.SHOT_USER
const PASS = process.env.SHOT_PASS
const CODE = process.env.SHOT_PROPERTY ?? 'UIREV'
const LABEL = process.env.SHOT_LABEL ?? 'admin'
const OUT = path.resolve(process.env.SHOT_OUT ?? 'screenshots', LABEL)
if (!USER || !PASS) { console.error('set SHOT_USER dan SHOT_PASS'); process.exit(1) }
fs.mkdirSync(OUT, { recursive: true })

// --- id nyata untuk rute dengan :id, lewat API
const login = await (await fetch(API + '/auth/login', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ tenant_code: TENANT, email: USER, password: PASS }) })).json()
const H = { authorization: 'Bearer ' + login.access_token }
const get = async (p) => { try { const r = await fetch(API + p, { headers: H }); return r.ok ? r.json() : null } catch { return null } }
const prop = (await get('/properties?limit=100'))?.data?.find((p) => p.code === CODE)
if (!prop) { console.error('properti ' + CODE + ' tidak ditemukan'); process.exit(1) }
const P = '/properties/' + prop.id
const first = async (p) => (await get(P + p))?.data?.[0]?.id
const ids = {
  reservation: await first('/reservations?limit=5&status=CONFIRMED') ?? await first('/reservations?limit=1'),
  folio: await first('/folios?limit=1'),
  stay: (await get(P + '/stays/in-house?limit=1'))?.data?.[0]?.id,
  guest: (await get('/guests?limit=1'))?.data?.[0]?.id,
  group: await first('/groups?limit=1'),
  cityLedger: await first('/city-ledger/accounts?limit=1'),
  budget: await first('/budgets?limit=1'),
  statement: await first('/bank/statements?limit=1'),
  user: (await get('/users?limit=1'))?.data?.[0]?.id,
  role: (await get('/roles?limit=1'))?.data?.[0]?.id,
}

// --- daftar rute (urut menurut menu); rute dengan :id dilewati bila id tidak ada
const routes = [
  ['/', 'beranda'], ['/reservations', 'reservasi'], ['/reservations/new', 'reservasi-baru'], ['/reservations/tape', 'tape-chart'], ['/availability', 'ketersediaan'],
  ['/arrivals', 'kedatangan'], ['/in-house', 'menginap'], ['/departures', 'keberangkatan'], ['/walk-in', 'walk-in'], ['/guests', 'tamu'], ['/groups', 'grup'],
  ['/room-status', 'status-kamar'], ['/housekeeping', 'housekeeping'], ['/housekeeping/tasks', 'daftar-bersih'], ['/maintenance', 'maintenance'], ['/lost-found', 'lost-found'], ['/room-blocks', 'blokir-kamar'],
  ['/folios', 'folio'], ['/cashier', 'kasir'], ['/cashier/shifts', 'shift-kasir'], ['/room-charges', 'room-charges'], ['/city-ledger', 'city-ledger'], ['/city-ledger/overdue', 'city-ledger-jatuh-tempo'],
  ['/night-audit', 'night-audit'], ['/reports', 'laporan'], ['/performance', 'kinerja'], ['/audit', 'audit-log'],
  ['/accounting/accounts', 'akun'], ['/accounting/mapping', 'akun-sistem'], ['/accounting/journals', 'jurnal'], ['/accounting/periods', 'periode'], ['/accounting/fiscal-years', 'tahun-fiskal'], ['/accounting/departments', 'departemen'],
  ['/accounting/trial-balance', 'neraca-saldo'], ['/accounting/ledger', 'buku-besar'], ['/accounting/income-statement', 'laba-rugi'], ['/accounting/department-report', 'laporan-departemen'], ['/accounting/balance-sheet', 'neraca'], ['/accounting/cash-flow', 'arus-kas'], ['/accounting/reconciliation', 'rekonsiliasi'],
  ['/payables/suppliers', 'supplier'], ['/payables/bills', 'tagihan-supplier'], ['/payables/credit-notes', 'nota-kredit'], ['/payables/payments', 'pembayaran-supplier'], ['/payables/aging', 'umur-hutang'],
  ['/tax/status', 'status-pkp'], ['/tax/profiles', 'profil-pajak'], ['/tax/returns', 'spt'], ['/tax/invoices', 'faktur-pajak'], ['/tax/liability', 'pajak-terutang'],
  ['/bank/accounts', 'rekening-bank'], ['/bank/statements', 'mutasi-bank'], ['/bank/cards', 'settlement-kartu'], ['/budget', 'anggaran'], ['/budget/vs-actual', 'anggaran-vs-aktual'],
  ['/setup/properties', 'set-properti'], ['/setup/properties/new', 'set-properti-baru'], ['/setup/users', 'set-pengguna'], ['/setup/users/new', 'set-pengguna-baru'], ['/setup/roles', 'set-peran'], ['/setup/roles/new', 'set-peran-baru'],
  ['/setup/room-types', 'set-tipe-kamar'], ['/setup/bed-types', 'set-tipe-ranjang'], ['/setup/rooms', 'set-kamar'], ['/setup/free-quotas', 'set-kuota-gratis'], ['/setup/taxes', 'set-pajak'], ['/setup/charge-codes', 'set-kode-tagihan'],
  ['/setup/rate-plans', 'set-rate-plan'], ['/setup/bed-supplements', 'set-suplemen-ranjang'], ['/setup/restrictions', 'set-restriksi'], ['/setup/yield-rules', 'set-yield'], ['/setup/rates', 'set-harga'], ['/setup/companies', 'set-perusahaan'],
  ['/account', 'akun-saya'], ['/halaman-tidak-ada', '404'],
  [ids.reservation && `/reservations/${ids.reservation}`, 'detail-reservasi'], [ids.folio && `/folios/${ids.folio}`, 'detail-folio'], [ids.stay && `/stays/${ids.stay}`, 'detail-stay'],
  [ids.guest && `/guests/${ids.guest}`, 'detail-tamu'], [ids.group && `/groups/${ids.group}`, 'detail-grup'], [ids.cityLedger && `/city-ledger/${ids.cityLedger}`, 'detail-city-ledger'],
  [ids.budget && `/budget/${ids.budget}`, 'detail-anggaran'], [ids.statement && `/bank/statements/${ids.statement}`, 'rekonsiliasi-bank'],
  [`/setup/properties/${prop.id}`, 'detail-properti'], [ids.user && `/setup/users/${ids.user}`, 'detail-pengguna'], [ids.role && `/setup/roles/${ids.role}`, 'detail-peran'],
].filter(([r]) => r)
const only = process.env.SHOT_ONLY?.split(',')
const list = only ? routes.filter(([r]) => only.includes(r)) : routes

const sizes = [
  { name: 'desktop', width: 1440, height: 900, dark: true },
  { name: 'laptop', width: 1366, height: 768 },
  { name: 'ponsel', width: 390, height: 844, mobile: true },
]

// Memilih properti review. Di ponsel pemilihnya ada di dalam drawer menu, bukan di top bar: buka drawer, pilih, tutup.
async function pickProperty(page, mobile) {
  const sel = '[data-testid="property-switcher"]'
  if (mobile) {
    await page.click('[data-testid="open-menu"]')
    await page.waitForSelector(`[data-testid="drawer"] ${sel}`, { timeout: 10000 }).catch(() => {})
  }
  await page.waitForSelector(`${sel} option:text-matches("${CODE}")`, { state: 'attached', timeout: 10000 }).catch(() => {})
  const opt = await page.$(`${sel} option:text-matches("${CODE}")`)
  if (opt) await page.selectOption(sel, await opt.getAttribute('value'))
  else console.log(`properti ${CODE} tidak ditemukan: halaman diambil untuk properti yang terpilih`)
  if (mobile) await page.keyboard.press('Escape')
  await page.waitForTimeout(800)
}
const browser = await chromium.launch()
const index = [`# Screenshot KamaraPMS (${LABEL})`, '', `Properti ${CODE}, ${new Date().toISOString()}`, '', '| No | Halaman | URL | Ukuran | File |', '|---|---|---|---|---|']
let no = 0
const settle = async (page) => {
  await page.waitForLoadState('networkidle').catch(() => {})
  await page.waitForFunction(() => !document.querySelector('[data-slot="skeleton"], [data-testid="loading"]'), null, { timeout: 8000 }).catch(() => {})
  await page.waitForTimeout(400)
}
for (const size of sizes) for (const scheme of size.dark ? ['light', 'dark'] : ['light']) {
  const ctx = await browser.newContext({ viewport: { width: size.width, height: size.height }, deviceScaleFactor: 1, isMobile: !!size.mobile, hasTouch: !!size.mobile, colorScheme: scheme, locale: 'id-ID' })
  // bahasa Indonesia lewat penyimpanan peramban: pemilih bahasa ada di menu avatar
  await ctx.addInitScript(() => { try { localStorage.setItem('pms.locale', 'id') } catch { /* tidak apa-apa */ } })
  const page = await ctx.newPage()
  await page.goto(WEB + '/login')
  await page.fill('input[name="tenant_code"]', TENANT)
  await page.fill('input[name="email"]', USER)
  await page.fill('input[name="password"]', PASS)
  await page.click('button[type="submit"]')
  await page.waitForURL((u) => !u.pathname.startsWith('/login'), { timeout: 15000 })
  await settle(page)
  await pickProperty(page, !!size.mobile)
  await settle(page)
  const pages = scheme === 'dark' ? list.filter(([r]) => ['/', '/room-status', '/reservations/tape', '/folios', '/night-audit'].includes(r) || r.startsWith('/folios/')) : list
  for (const [route, name] of pages) {
    no++
    const file = `${String(no).padStart(3, '0')}-${name}-${size.name}${scheme === 'dark' ? '-gelap' : ''}.png`
    try {
      await page.goto(WEB + route)
      await settle(page)
      await page.screenshot({ path: path.join(OUT, file), fullPage: true })
      index.push(`| ${no} | ${name} | ${route} | ${size.name}${scheme === 'dark' ? ' gelap' : ''} | ${file} |`)
      console.log('ok', file)
    } catch (e) { console.log('gagal', route, size.name, e.message.split('\n')[0]) }
  }
  // keadaan khusus, sekali per ukuran (mode terang)
  if (scheme === 'light') {
    const extra = async (name, fn) => { no++; const file = `${String(no).padStart(3, '0')}-${name}-${size.name}.png`; try { await fn(); await page.waitForTimeout(500); await page.screenshot({ path: path.join(OUT, file), fullPage: false }); index.push(`| ${no} | ${name} | — | ${size.name} | ${file} |`); console.log('ok', file) } catch (e) { console.log('gagal', name, e.message.split('\n')[0]) } await page.keyboard.press('Escape').catch(() => {}) }
    await page.goto(WEB + '/'); await settle(page)
    await extra('pencarian-ctrl-k', async () => { await page.keyboard.press('Control+k') })
    if (size.mobile) await extra('menu-drawer', async () => { await page.click('[data-testid="open-menu"]') })
    await page.goto(WEB + '/room-status'); await settle(page)
    await extra('sheet-kamar', async () => { await page.click('[data-testid^="tile-"]') })
    await page.goto(WEB + '/arrivals'); await settle(page)
    await extra('dialog-kedatangan', async () => { await page.locator('main button:visible').filter({ hasText: /check|masuk/i }).first().click({ timeout: 5000 }) })
  }
  await ctx.close()
}
await browser.close()
fs.writeFileSync(path.join(OUT, 'index.md'), index.join('\n') + '\n')
console.log(`\nselesai: ${no} file di ${OUT}`)
