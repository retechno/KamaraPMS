import { test, expect } from '../support/fixtures'
import { uid } from '../support/factories'
import { tx } from '../support/i18n'
import { pickSelect } from '../support/ui'
import { ApiClient } from '../support/api'

/** The master data a hotel sets up before it opens: room types, rooms, charge codes, rate plans, roles and users. */
test.describe('Setup properti', () => {
  test('SET-01 membuat tipe kamar baru', async ({ page, loginAs, data }) => {
    const code = `T${uid()}`.slice(0, 8)
    await loginAs('admin', '/setup/room-types')
    await page.getByTestId('new-type').click()
    await page.locator('input[name=code]').fill(code)
    await page.locator('input[name=name]').fill(`Tipe Uji ${code}`)
    await page.locator('input[name=max_adult]').fill('2')
    await page.locator('input[name=max_child]').fill('1')
    await page.locator('input[name=max_occupancy]').fill('3')
    await page.locator('input[name=base_occupancy]').fill('2')
    await page.getByRole('button', { name: tx('common.save'), exact: true }).click()
    await expect(page.getByTestId(`type-${code}`)).toBeVisible()
    const types = (await data.api.get<{ data: { code: string; max_occupancy: number }[] }>(`${data.P}/room-types`)).data
    expect(types.find((t) => t.code === code)?.max_occupancy).toBe(3)
  })

  test('SET-02 validasi: kapasitas maksimum melebihi dewasa + anak ditolak', async ({ page, loginAs }) => {
    const code = `X${uid()}`.slice(0, 8)
    await loginAs('admin', '/setup/room-types')
    await page.getByTestId('new-type').click()
    await page.locator('input[name=code]').fill(code)
    await page.locator('input[name=name]').fill('Tidak sah')
    await page.locator('input[name=max_adult]').fill('1')
    await page.locator('input[name=max_child]').fill('0')
    await page.locator('input[name=max_occupancy]').fill('4')
    await page.locator('input[name=base_occupancy]').fill('1')
    await page.getByRole('button', { name: tx('common.save'), exact: true }).click()
    await expect(page.locator('[aria-invalid=true], [data-testid=form-error]').first()).toBeVisible()
    await expect(page.getByTestId(`type-${code}`)).toHaveCount(0)
  })

  test('SET-03 menambah kamar baru pada tipe yang ada', async ({ page, loginAs, seed, data }) => {
    const number = `9${String(Date.now()).slice(-3)}`
    const type = seed.roomTypes[0]!
    await loginAs('admin', '/setup/rooms')
    await page.getByTestId('new-room').click()
    await page.locator('input[name=room_number]').fill(number)
    await pickSelect(page, 'room_type_id', type.code)
    await page.locator('input[name=floor]').fill('9')
    const bed = page.locator('select[name=bed_type_id]')
    if (await bed.count()) await bed.selectOption({ index: 1 })
    await page.getByRole('button', { name: tx('common.save'), exact: true }).click()
    await expect(page.getByTestId(`room-${number}`)).toBeVisible()
    const rooms = await data.api.all<{ room_number: string; room_type_id: number }>(`${data.P}/rooms`)
    expect(rooms.find((r) => r.room_number === number)?.room_type_id).toBe(type.id)
  })

  test('SET-04 membuat kode charge (minibar) dengan akun pendapatan', async ({ page, loginAs, data }) => {
    const code = `MB${uid()}`.slice(0, 8)
    const codes = (await data.api.get<{ data: { charge_type: string; gl_account_code: string | null }[] }>(`${data.P}/charge-codes`)).data
    const account = codes.find((c) => c.charge_type === 'FOOD_BEVERAGE' && c.gl_account_code)?.gl_account_code ?? codes.find((c) => c.gl_account_code)?.gl_account_code ?? ''
    await loginAs('admin', '/setup/charge-codes')
    await page.getByTestId('new-code').click()
    const form = page.getByTestId('code-form')
    await form.locator('input[name=code]').fill(code)
    await form.locator('input[name=name]').fill(`Minibar ${code}`)
    await form.locator('select[name=charge_type]').selectOption('FOOD_BEVERAGE')
    await form.locator('input[name=default_unit_price]').fill('35000')
    if (account) await form.locator('input[name=gl_account_code]').fill(account)
    await form.getByRole('button', { name: tx('common.save'), exact: true }).click()
    await expect(page.getByTestId(`code-${code}`)).toBeVisible()
  })

  test('SET-05 membuat rate plan baru yang dijual lewat kode ROOM', async ({ page, loginAs, data }) => {
    const code = `RP${uid()}`.slice(0, 8)
    await loginAs('admin', '/setup/rate-plans')
    await page.getByTestId('new-plan').click()
    const form = page.getByTestId('plan-form')
    await form.locator('input[name=code]').fill(code)
    await form.locator('input[name=name]').fill(`Paket Uji ${code}`)
    await form.locator('select[name=meal_plan]').selectOption('BB')
    await pickSelect(form, 'room_charge_code_id', 'ROOM')
    await form.getByRole('button', { name: tx('common.save'), exact: true }).click()
    await expect(page.getByTestId(`plan-${code}`)).toBeVisible()
    const plans = (await data.api.get<{ data: { code: string; meal_plan: string }[] }>(`${data.P}/rate-plans`)).data
    expect(plans.find((p) => p.code === code)?.meal_plan).toBe('BB')
  })

  test('SET-06 membuat user resepsionis baru dan user itu bisa login', async ({ page, loginAs, seed }) => {
    const email = `baru.${uid().toLowerCase()}@e2e.example`
    const password = 'Password-Baru-123!'
    await loginAs('admin', '/setup/users/new')
    await page.locator('input[name=email]').fill(email)
    await page.locator('input[name=full_name]').fill('Pengguna Baru E2E')
    await page.locator('input[name=password]').fill(password)
    await page.getByRole('button', { name: tx('users.addProperty') }).click()
    await page.getByLabel(tx('users.propertyN', { n: 1 })).selectOption(String(seed.propertyId))
    await page.getByLabel(tx('users.roleN', { n: 1 })).selectOption({ label: 'E2E Resepsionis' })
    await page.getByRole('button', { name: tx('users.create') }).click()
    await expect(page).not.toHaveURL(/\/setup\/users\/new$/)
    const api = await ApiClient.login(email, password)
    const me = await api.get<{ properties: { code: string; permissions: string[] }[] }>('/auth/me')
    expect(me.properties.find((p) => p.code === seed.propertyCode)?.permissions).toContain('frontdesk.checkin')
  })

  test('SET-07 membuat peran baru dengan izin housekeeping', async ({ page, loginAs, admin }) => {
    const name = `Peran Uji ${uid()}`
    await loginAs('admin', '/setup/roles/new')
    await page.locator('input[name=name]').fill(name)
    await page.locator('input[name=description]').fill('Dibuat oleh test E2E')
    // the group of a permission is its "group" in internal/platform/auth/permissions.go: housekeeping.update is in "Rooms"
    await page.getByTestId('group-check-Rooms').check()
    await page.getByTestId('save').click()
    await expect.poll(async () => (await admin.get<{ data: { name: string; permissions: string[] }[] }>('/roles')).data.find((r) => r.name === name)?.permissions ?? []).toContain('housekeeping.update')
  })
})
