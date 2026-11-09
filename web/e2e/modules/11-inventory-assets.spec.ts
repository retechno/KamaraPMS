import { test, expect } from '../support/fixtures'
import { addDays, uid } from '../support/factories'
import { setDate } from '../support/ui'

/** Inventory (demo of pms-seed inventory) and the fixed asset register (demo of pms-seed assets). */
test.describe('Inventory dan aset', () => {
  test('INV-01 dashboard inventory menampilkan nilai stok dari saldo awal', async ({ page, loginAs }) => {
    await loginAs('admin', '/inventory')
    await expect(page.getByTestId('dashboard')).toBeVisible()
    await expect(page.getByTestId('dash-value')).toBeVisible()
  })

  test('INV-02 membuat barang baru dengan kategori dan satuan stok', async ({ page, loginAs, data }) => {
    const code = `BRG${uid()}`.slice(0, 10)
    await loginAs('admin', '/inventory/items/new')
    const form = page.getByTestId('item-form')
    await form.locator('input[name=code]').fill(code)
    await form.locator('input[name=name]').fill(`Sabun cair uji ${code}`)
    await form.locator('select[name=category_id]').selectOption({ index: 1 })
    await form.locator('select[name=stock_unit_id]').selectOption({ index: 1 })
    await form.locator('input[name=reference_cost]').fill('18500')
    await page.getByTestId('item-save').click()
    await expect(page).toHaveURL(/\/inventory\/items\/\d+$/)
    const items = (await data.api.get<{ items: { code: string; reference_cost: string | null }[] }>(`${data.P}/inventory/items`, { q: code })).items
    expect(Number(items.find((i) => i.code === code)?.reference_cost)).toBe(18500)
  })

  test('INV-03 daftar barang bisa dicari', async ({ page, loginAs, data }) => {
    const items = (await data.api.get<{ items: { code: string; name: string }[] }>(`${data.P}/inventory/items`, { limit: 5 })).items
    expect(items.length, 'pms-seed inventory made items').toBeGreaterThan(0)
    await loginAs('admin', '/inventory/items')
    await page.locator('input[name=q], input[type=search]').first().fill(items[0]!.code)
    await page.locator('input[name=q], input[type=search]').first().press('Enter')
    await expect(page.getByTestId(`item-${items[0]!.code}`)).toBeVisible()
  })

  test('INV-04 permintaan pembelian (purchase request) dibuat dan diajukan', async ({ page, loginAs, data }) => {
    // the first level of approval is the head of the department: without one, a request cannot be submitted (it stays a draft)
    const rooms = (await data.api.get<{ data: { id: number; code: string; head_user_id: number | null }[] }>(`${data.P}/departments`)).data.find((d) => d.code === 'ROOMS')!
    if (!rooms.head_user_id) await data.api.patch(`${data.P}/departments/${rooms.id}`, { head_user_id: data.api.userId })
    await loginAs('admin', '/inventory/requests/new')
    const form = page.getByTestId('request-form')
    await form.locator('select[name=department_id]').selectOption(String(rooms.id))
    await form.locator('select[name=warehouse_id]').selectOption({ index: 1 })
    await setDate(form, 'needed_on', addDays(await data.businessDate(), 3))
    await expect(form.locator('select[name=item_0]').or(form.getByTestId('line-add')).first()).toBeVisible()
    if (!(await form.locator('select[name=item_0]').count())) await form.getByTestId('line-add').click()
    await form.locator('select[name=item_0]').selectOption({ index: 1 })
    await form.locator('input[name=qty_0]').fill('12')
    const price = form.locator('input[name=price_0]')
    if (!(await price.inputValue())) await price.fill('15000')
    await page.getByTestId('request-submit').click()
    await expect(page).toHaveURL(/\/inventory\/requests\/\d+$/)
    await expect(page.getByTestId('request-status')).toBeVisible()
    const id = Number(page.url().split('/').pop())
    const req = await data.api.get<{ status: string }>(`${data.P}/inventory/requests/${id}`)
    expect(req.status).not.toBe('DRAFT')
  })

  test('INV-05 stock take (opname) dan gudang hasil seed tampil', async ({ page, loginAs, data }) => {
    const wh = (await data.api.get<{ data?: { code: string }[]; warehouses?: { code: string }[] }>(`${data.P}/inventory/warehouses`))
    const list = wh.data ?? wh.warehouses ?? []
    await loginAs('admin', '/inventory/warehouses')
    if (list[0]) await expect(page.getByTestId(`warehouse-${list[0].code}`)).toBeVisible()
    await page.goto('/inventory/counts')
    await expect(page.locator('main h1')).toBeVisible()
  })

  test('AST-01 register aset menampilkan aset saldo awal dan membuka detailnya', async ({ page, loginAs, data }) => {
    const assets = (await data.api.get<{ data: { id: number; number: string; name: string; status: string }[] }>(`${data.P}/assets`, { status: 'ACTIVE', limit: 50 })).data
    expect(assets.length, 'pms-seed assets made the opening balances').toBeGreaterThanOrEqual(10)
    await loginAs('admin', '/assets')
    await page.getByTestId('filter-q').fill(assets[0]!.number)
    await expect(page.getByTestId('asset-table')).toContainText(assets[0]!.number)
    await page.goto(`/assets/${assets[0]!.id}`)
    await expect(page.locator('main h1')).toContainText(assets[0]!.name) // the name is the title; the data card holds location, dates and the rest
    await expect(page.getByTestId('asset-data')).toBeVisible()
  })

  test('AST-02 memindahkan aset ke lokasi lain dengan alasan', async ({ page, loginAs, data }) => {
    const asset = (await data.api.get<{ data: { id: number; location_id: number | null; status: string }[] }>(`${data.P}/assets`, { status: 'ACTIVE', limit: 50 })).data.find((a) => a.location_id !== null)!
    await loginAs('admin', `/assets/${asset.id}`)
    await page.getByTestId('act-move').click()
    const dialog = page.getByTestId('move-dialog')
    await dialog.getByTestId('move-location-on').check()
    const options = dialog.getByTestId('move-location').locator('option')
    const count = await options.count()
    let target = ''
    for (let i = 0; i < count; i++) {
      const v = (await options.nth(i).getAttribute('value')) ?? ''
      if (v && v !== '0' && v !== String(asset.location_id)) { target = v; break }
    }
    await dialog.getByTestId('move-location').selectOption(target)
    await dialog.locator('input[name=reason], textarea[name=reason]').first().fill('Pindah ke lobby (E2E)')
    await dialog.getByTestId('move-confirm').click()
    await expect.poll(async () => String((await data.api.get<{ location_id: number | null }>(`${data.P}/assets/${asset.id}`)).location_id)).toBe(target)
  })
})
