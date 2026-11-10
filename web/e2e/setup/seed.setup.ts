import { execFileSync } from 'node:child_process'
import { mkdirSync, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { test as setup, expect } from 'playwright/test'
import { ApiClient, ApiCallError } from '../support/api'
import { ADMIN, PROPERTY_CODE, STATE_FILE, TENANT, TZ, USER_PASSWORD } from '../support/env'
import { ROLES, type RoleKey, type SeedState } from '../support/state'

/**
 * Builds the world every test starts from, on the database of scripts/e2e.sh (tenant E2E). Safe to run again: what exists is kept.
 *  - the property E2E (business day opened yesterday, so the night audit of the final project may run today), no incidental deposit at check-in
 *  - the demo data of pms-seed: 50 rooms over 4 types, the rate plan RO for 60 days, POS outlets and menu, inventory, banquet rooms, fixed assets, night audit recipients
 *  - a role and a user for each desk (support/state.ts ROLES), so the menus and the 403s are tested as the people who use them
 */
const root = fileURLToPath(new URL('../../../', import.meta.url))

function localDate(offsetDays = 0): string {
  return new Intl.DateTimeFormat('en-CA', { timeZone: TZ }).format(new Date(Date.now() + offsetDays * 864e5))
}

function pmsSeed(args: string[]): void {
  const bin = process.env.E2E_BIN
  const exe = process.env.E2E_EXE ?? (process.platform === 'win32' ? '.exe' : '')
  const [cmd, argv] = bin ? [`${bin}/pms-seed${exe}`, args] : ['go', ['run', './cmd/pms-seed', ...args]]
  execFileSync(cmd, argv, {
    cwd: root,
    stdio: ['ignore', 'ignore', 'inherit'],
    env: { ...process.env, PMS_DATABASE_URL: process.env.PMS_E2E_DATABASE_URL ?? 'postgres://pms:pms@localhost:55432/pms_e2e?sslmode=disable' },
    timeout: 300_000,
  })
}

setup('seed: property, demo data, roles and users', async () => {
  setup.setTimeout(600_000)
  const admin = await ApiClient.login(ADMIN.email, ADMIN.password)

  // --- property
  let prop = (await admin.get<{ data: { id: number; code: string }[] }>('/properties', { limit: 200 })).data.find((p) => p.code === PROPERTY_CODE)
  if (!prop) {
    prop = await admin.post('/properties', {
      code: PROPERTY_CODE, name: 'Hotel E2E Ubud', address: 'Jl. Pengujian No. 1', city: 'Gianyar', country_code: 'ID', phone: '+62 361 000 000', email: 'stay@e2e.example',
      timezone: TZ, currency_code: 'IDR', currency_decimals: 0, check_in_time: '14:00', check_out_time: '12:00', night_audit_earliest_time: '00:00',
      require_room_inspection_for_checkin: false, checkin_incidental_deposit: '0', opening_business_date: localDate(-1), data_language: 'id',
    })
  }
  const propertyId = prop!.id
  const P = `/properties/${propertyId}`

  // --- demo data through the same services the application uses (cmd/pms-seed); each one is idempotent
  const base = ['-tenant', TENANT, '-property', PROPERTY_CODE]
  pmsSeed(['rooms', ...base, '-floors', '5', '-per-floor', '10'])
  pmsSeed(['rates', ...base, '-days', '60'])
  for (const demo of ['pos', 'inventory', 'banquet', 'assets', 'nightaudit']) pmsSeed([demo, ...base])

  // --- roles and users
  const roles = (await admin.get<{ data: { id: number; name: string }[] }>('/roles')).data
  const users: SeedState['users'] = { admin: { email: ADMIN.email, password: ADMIN.password } } as SeedState['users']
  const existing = await admin.all<{ email: string }>('/users')
  for (const [key, def] of Object.entries(ROLES) as [RoleKey, (typeof ROLES)[RoleKey]][]) {
    let role = roles.find((r) => r.name.toLowerCase() === def.name.toLowerCase())
    if (!role) role = await admin.post('/roles', { name: def.name, description: 'E2E', permissions: def.permissions })
    const email = `${key.toLowerCase()}@e2e.example` // keys such as invHead: the server keeps e-mail addresses in lower case
    if (!existing.some((u) => u.email.toLowerCase() === email)) {
      try {
        await admin.post('/users', { email, full_name: def.fullName, password: USER_PASSWORD, grants: [{ property_id: propertyId, role_id: role!.id }] })
      } catch (e) {
        if (!(e instanceof ApiCallError && e.code === 'EMAIL_TAKEN')) throw e
      }
    }
    users[key] = { email, password: USER_PASSWORD }
  }

  // --- the head of every department: the first level of a purchase request (a department without a head cannot be asked)
  const head = (await admin.all<{ id: number; email: string }>('/users')).find((u) => u.email.toLowerCase() === users.invHead.email.toLowerCase())
  expect(head, 'the user invHead exists').toBeTruthy()
  for (const d of (await admin.get<{ data: { id: number; head_user_id: number | null }[] }>(`${P}/departments`)).data) {
    if (head && d.head_user_id !== head.id) await admin.patch(`${P}/departments/${d.id}`, { head_user_id: head.id })
  }

  // --- what the tests need to know
  const bd = await admin.get<{ business_date: string }>(`${P}/business-date`)
  const plans = (await admin.get<{ data: { id: number; code: string }[] }>(`${P}/rate-plans`)).data
  const ratePlan = plans.find((p) => p.code === 'RO') ?? plans[0]
  const roomTypes = (await admin.get<{ data: { id: number; code: string; name: string }[] }>(`${P}/room-types`)).data
  const rooms = await admin.all<{ id: number }>(`${P}/rooms`)
  expect(ratePlan, 'pms-seed rates made the rate plan RO').toBeTruthy()
  expect(rooms.length, 'pms-seed rooms made the rooms').toBeGreaterThanOrEqual(40)

  const state: SeedState = {
    propertyId, propertyCode: PROPERTY_CODE, openingBusinessDate: bd.business_date,
    ratePlanId: ratePlan!.id, roomTypes: roomTypes.map((t) => ({ id: t.id, code: t.code, name: t.name })), users,
  }
  mkdirSync(new URL('.', STATE_FILE), { recursive: true })
  writeFileSync(STATE_FILE, JSON.stringify(state, null, 2))
})
