// Data contoh untuk review UI. HANYA untuk database DEVELOPMENT.
// Langkah:
//   1) node scripts/ui-review/seed-demo.mjs property     -> membuat properti UIREV (hari bisnis dibuka kemarin)
//   2) go run ./cmd/pms-seed rooms -tenant DEMO -property UIREV -floors 5 -per-floor 10
//      go run ./cmd/pms-seed rates -tenant DEMO -property UIREV -days 60
//   3) node scripts/ui-review/seed-demo.mjs data         -> tamu, reservasi, check-in, pembayaran, night audit, hari ini
// Env: PMS_API (default http://127.0.0.1:18080), SHOT_TENANT (DEMO), SHOT_USER, SHOT_PASS, SHOT_PROPERTY (UIREV)
const BASE = (process.env.PMS_API ?? 'http://127.0.0.1:18080') + '/api/v1'
const TENANT = process.env.SHOT_TENANT ?? 'DEMO'
const CODE = process.env.SHOT_PROPERTY ?? 'UIREV'
const TZ = 'Asia/Makassar'
let token = ''
async function login() {
  const r = await fetch(BASE + '/auth/login', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ tenant_code: TENANT, email: process.env.SHOT_USER, password: process.env.SHOT_PASS }) })
  const j = await r.json(); if (!j.access_token) throw new Error('login gagal: ' + JSON.stringify(j)); token = j.access_token
}
async function api(method, path, body, soft = true) {
  const r = await fetch(BASE + path, { method, headers: { 'content-type': 'application/json', authorization: 'Bearer ' + token, ...(method !== 'GET' ? { 'idempotency-key': crypto.randomUUID() } : {}) }, body: body ? JSON.stringify(body) : undefined })
  const t = await r.text(); let j; try { j = JSON.parse(t) } catch { j = t }
  if (!r.ok) { const m = `${method} ${path} -> ${r.status} ${typeof j === 'string' ? j : (j.code ?? '') + ' ' + (j.detail ?? '')}`; if (soft) { console.log('  lewati:', m.slice(0, 200)); return null } throw new Error(m) }
  return j
}
const localDate = (offsetDays = 0) => { const d = new Date(Date.now() + offsetDays * 864e5); return new Intl.DateTimeFormat('en-CA', { timeZone: TZ }).format(d) }
const addDays = (iso, n) => { const d = new Date(iso + 'T00:00:00Z'); d.setUTCDate(d.getUTCDate() + n); return d.toISOString().slice(0, 10) }

await login()
const step = process.argv[2]
if (step === 'property') {
  const p = await api('POST', '/properties', { code: CODE, name: 'Kamara Ubud (review UI)', address: 'Jl. Raya Ubud No. 18', city: 'Gianyar', country_code: 'ID', phone: '+62 361 975 000', email: 'stay@kamara.test', timezone: TZ, currency_code: 'IDR', currency_decimals: 0, check_in_time: '14:00', check_out_time: '12:00', night_audit_earliest_time: '00:00', opening_business_date: localDate(-1) }, false)
  console.log(`properti ${p.code} dibuat (id ${p.id}). Lanjut: pms-seed rooms & rates untuk -property ${CODE}, lalu: node scripts/ui-review/seed-demo.mjs data`)
  process.exit(0)
}
if (step !== 'data') { console.log('pakai: seed-demo.mjs property | data'); process.exit(1) }

const props = (await api('GET', '/properties?limit=100', null, false)).data
const prop = props.find((p) => p.code === CODE); if (!prop) throw new Error('properti ' + CODE + ' tidak ada; jalankan langkah property dulu')
const P = '/properties/' + prop.id
const bd = await api('GET', P + '/business-date', null, false)
const d0 = bd.business_date ?? bd.date; const d1 = addDays(d0, 1)
console.log('hari bisnis', d0)
const rooms = (await api('GET', P + '/rooms?limit=200', null, false)).data
if (!rooms.length) throw new Error('belum ada kamar: jalankan pms-seed rooms')
const plan = (await api('GET', P + '/rate-plans', null, false)).data.find((p) => p.code === 'RO') ?? (await api('GET', P + '/rate-plans', null, false)).data[0]
const typeIds = [...new Set(rooms.map((r) => r.room_type_id))]
const names = ['Ayu Pratiwi','Daniel Hartono','Made Wirawan','Sarah Lim','Budi Santoso','Rina Kusuma','James Walker','Siti Rahma','Hendra Gunawan','Lisa Morgan','Kevin Tan','Putu Ardana','Nadia Putri','Thomas Becker','Yuki Tanaka','Agus Salim','Dewi Lestari','Michael Chen','Wayan Sudarsana','Emma Johansson','Rizky Ramadhan','Olivia Brown','Fajar Nugroho','Mei Ling','Komang Arya','Lucas Martin','Intan Permata','Arjun Mehta','Gede Prasetya','Sophie Dubois','Andi Wijaya','Hannah Schmidt','Ketut Suardana','Chloe Wilson','Bayu Saputra','Laura Rossi','Eka Saraswati','Noah Smith','Citra Anggraini','Mateo Garcia','Dimas Prakoso','Isabella Costa','Ratna Sari','Oliver Taylor','Yoga Pratama','Amelia Clark','Nyoman Darma','Ben Harris','Tari Wulandari','Ethan Lee']
const guests = []
for (const n of names) { const [f, ...l] = n.split(' '); const g = await api('POST', '/guests', { origin_property_id: prop.id, first_name: f, last_name: l.join(' '), email: `${f}.${l.join('')}@mail.test`.toLowerCase(), phone: '+62812' + String(1000000 + guests.length * 7919).slice(0, 7) }); if (g) guests.push(g.id) }
let gi = 0; const nextGuest = () => guests[gi++ % guests.length]
const srcs = ['OTA', 'WEBSITE', 'PHONE', 'OTA', 'EMAIL', 'AGENT', 'OTA']
async function book(type, arr, dep, room, o = {}) {
  const body = { guest_id: nextGuest(), source: srcs[gi % srcs.length], confirm: !o.draft, rooms: [{ room_type_id: type, rate_plan_id: plan.id, arrival_date: arr, departure_date: dep, adult_count: 2, ...(room && !o.draft ? { room_id: room.id } : {}) }] }
  if (o.special) body.special_request = o.special
  if (o.company) body.company_id = o.company
  return api('POST', P + '/reservations', body)
}
async function checkIn(res, room) {
  await api('POST', `${P}/rooms/${room.id}/housekeeping`, { status: 'CLEAN' }) // boleh gagal bila sudah siap
  const full = await api('GET', `${P}/reservations/${res.id}`)
  return api('POST', `${P}/reservations/${res.id}/rooms/${full.rooms[0].id}/check-in`, { version: full.version, guest_id: full.guest_id, adult_count: 2, room_id: room.id })
}
// --- hari d0 (kemarin): 60% terisi
const free = [...rooms]
const n0 = Math.round(rooms.length * 0.6)
const depsD0 = [1, 1, 1, 1, 1, 2, 2, 3, 3, 4, 5]
let ci0 = 0
for (let i = 0; i < n0; i++) { const room = free.shift(); const r = await book(room.room_type_id, d0, addDays(d0, depsD0[i % depsD0.length]), room); if (r && (await checkIn(r, room))) ci0++ }
console.log('check-in kemarin:', ci0)
await api('POST', P + '/cashier/shifts', { opening_float: '2000000' })
let ih = (await api('GET', P + '/stays/in-house?limit=200', null, false)).data
const methods = ['CARD', 'BANK_TRANSFER', 'CARD', 'CASH', 'OTHER']
ih.forEach(() => {})
for (let i = 0; i < ih.length; i++) if (i % 3 !== 2) await api('POST', `${P}/folios/${ih[i].balance.folios[0].id}/payments`, { amount: String(500000 + (i % 4) * 250000), payment_method: methods[i % methods.length], remarks: 'Deposit' })
const cur = await api('GET', P + '/cashier/shifts/current')
const shiftId = cur?.id ?? cur?.shift?.id
if (shiftId) { const sh = await api('GET', `${P}/cashier/shifts/${shiftId}`); await api('POST', `${P}/cashier/shifts/${shiftId}/close`, { counted_cash: sh.cash.expected }) }
const run = await api('POST', P + '/night-audit/run', { business_date: d0 })
console.log(run ? 'night audit ' + d0 + ' selesai' : 'night audit gagal: periksa /night-audit di aplikasi')
// --- hari d1 (hari ini)
await api('POST', P + '/cashier/shifts', { opening_float: '2000000' })
ih = (await api('GET', P + '/stays/in-house?limit=200', null, false)).data
let co = 0
for (const s of ih.filter((x) => x.stay.departure_date === d1).slice(0, 4)) {
  const bal = Number(s.balance.amount); if (bal > 0) await api('POST', `${P}/folios/${s.balance.folios[0].id}/payments`, { amount: String(bal), payment_method: 'CARD' })
  if (bal < 0) continue // perlu refund: biarkan sebagai keberangkatan yang belum selesai
  const full = await api('GET', `${P}/stays/${s.id}`); if (await api('POST', `${P}/stays/${s.id}/check-out`, { version: full?.version ?? full?.stay?.version ?? s.version })) co++
}
console.log('check-out hari ini:', co)
ih = (await api('GET', P + '/stays/in-house?limit=200', null, false)).data
const occ = new Set(ih.map((s) => s.room.id))
const vacant = rooms.filter((r) => !occ.has(r.id))
const company = await api('POST', P + '/companies', { code: 'NUSA', name: 'PT Nusa Wisata Indonesia', contact_name: 'Ibu Ratna', email: 'finance@nusawisata.test', credit_limit: '50000000', payment_terms_days: 30 })
const specials = ['Lantai atas, jauh dari lift', 'Tiba larut malam', 'Ulang tahun pernikahan', '', 'Butuh tempat tidur bayi', '']
const arr = []
for (let i = 0; i < 14; i++) { const t = typeIds[i % typeIds.length]; const room = i < 9 ? vacant.splice(vacant.findIndex((r) => r.room_type_id === t), 1)[0] : undefined; const r = await book(t, d1, addDays(d1, [2, 3, 1, 4, 5][i % 5]), room, { special: specials[i % 6] || undefined, company: i === 3 ? company?.id : undefined }); if (r) arr.push({ r, room }) }
let ci1 = 0; for (const a of arr.slice(0, 5)) if (a.room && (await checkIn(a.r, a.room))) ci1++
console.log('kedatangan hari ini:', arr.length, 'sudah check-in:', ci1)
const perDay = [0, 10, 14, 16, 8, 5, 4, 6, 9, 14, 16, 18, 7, 4]
let fut = 0
for (let d = 1; d < 14; d++) for (let k = 0; k < perDay[d]; k++) { const r = await book(typeIds[(d + k) % typeIds.length], addDays(d1, d), addDays(d1, d + 1 + (k % 3)), undefined, { draft: k === 7 && d % 3 === 0 }); if (r) fut++ }
console.log('reservasi ke depan:', fut)
const c = await book(typeIds[1], addDays(d1, 3), addDays(d1, 5)); if (c) await api('POST', `${P}/reservations/${c.id}/cancel`, { version: c.version, reason: 'Tamu membatalkan perjalanan' })
const hk = (await api('GET', P + '/housekeeping?limit=200', null, false)).data
const seq = ['CLEANING', 'CLEAN', 'INSPECTED', 'CLEAN']; let k = 0
for (const r of hk) if (r.occupancy !== 'OCCUPIED' && r.status === 'DIRTY' && k < 12) { const s = seq[k++ % 4]; await api('POST', `${P}/rooms/${r.room_id}/housekeeping`, { status: s === 'INSPECTED' ? 'CLEAN' : s }); if (s === 'INSPECTED') await api('POST', `${P}/rooms/${r.room_id}/housekeeping`, { status: 'INSPECTED' }) }
for (const r of [...vacant].reverse()) { const b = await api('POST', P + '/room-blocks', { room_id: r.id, block_type: 'OOO', start_date: d1, end_date: addDays(d1, 3), reason: 'Perbaikan AC' }); if (b) { await api('POST', P + '/maintenance-requests', { room_id: r.id, category: 'AC', description: 'AC tidak dingin, bunyi berisik', priority: 'HIGH' }); break } }
await api('POST', P + '/maintenance-requests', { room_id: rooms[21].id, category: 'PLUMBING', description: 'Keran wastafel bocor', priority: 'NORMAL' })
await api('POST', P + '/lost-found', { description: 'Kacamata hitam', category: 'OTHER', room_id: rooms[11].id, storage_location: 'Laci FO 2', possible_owner: 'Daniel Hartono' })
await api('POST', P + '/lost-found', { description: 'Charger laptop USB-C', category: 'ELECTRONICS', room_id: rooms[30].id, storage_location: 'Laci FO 1' })
ih = (await api('GET', P + '/stays/in-house?limit=200', null, false)).data
for (let i = 0; i < Math.min(10, ih.length); i++) await api('POST', `${P}/folios/${ih[i].balance.folios[0].id}/payments`, { amount: String(350000 + (i % 3) * 400000), payment_method: ['CARD', 'CASH', 'BANK_TRANSFER', 'OTHER'][i % 4] })
console.log('selesai. Buka aplikasi dan pilih properti', CODE)
