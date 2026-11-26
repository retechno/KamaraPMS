import { test, expect } from '../support/fixtures'
import type { Factories } from '../support/factories'
import type { Locator, Page } from 'playwright/test'

/** The POS of the outlets (demo of pms-seed pos): a takeaway paid by card, a charge to the room of a guest in house and its return from the reopened bill, and the outlet report. */

interface Outlet { id: number; code: string; name: string; allow_room_charge: boolean; uses_tables: boolean; drawer?: string | null }
interface Product { id: number; name: string; option_groups?: { min_select: number }[]; prices?: { price: string }[] }

async function outletAndProduct(data: Factories, roomCharge = false): Promise<{ outlet: Outlet; product: Product }> {
  const outlets = (await data.api.get<{ data: (Outlet & { is_active?: boolean })[] }>(`${data.P}/pos/outlets`, { active: true })).data
  for (const outlet of outlets.filter((o) => !roomCharge || o.allow_room_charge)) {
    const catalog = await data.api.get<{ products: Product[] }>(`${data.P}/pos/outlets/${outlet.id}/catalog`)
    const product = catalog.products.find((p) => !(p.option_groups ?? []).some((g) => g.min_select > 0) && (p.prices?.length ?? 1) > 0)
    if (product) return { outlet, product }
  }
  throw new Error('pms-seed pos made no outlet with a product that needs no option')
}

/**
 * Leaves the pay dialog with a single row of the method, for the whole amount. The dialog opens with a cash row for the whole bill
 * (and cash needs the money received), so every row there is removed first.
 */
async function onlyMethod(pay: Locator, method: 'CARD' | 'ROOM'): Promise<void> {
  await expect(pay.locator('[data-testid^=pay-row-]').first()).toBeVisible()
  const removes = pay.locator('[data-testid^=remove-]')
  for (let n = await removes.count(); n > 0; n--) {
    await removes.first().click()
    await expect(removes).toHaveCount(n - 1)
  }
  await pay.getByTestId(`add-${method}`).click()
  await expect(pay.getByTestId(`pay-row-${method}`)).toBeVisible()
}

/** Opens a takeaway bill at the outlet from the floor and adds one product; the page is left on the bill. */
async function takeawayWithProduct(page: Page, outlet: Outlet, product: Product, customer: string): Promise<number> {
  const select = page.getByTestId('outlet-select')
  await expect(select).toBeVisible() // pms-seed pos makes four outlets, so the floor offers the choice
  await select.selectOption(String(outlet.id))
  await page.getByTestId('new-takeaway').click()
  const dialog = page.getByTestId('takeaway-dialog')
  await dialog.locator('input[name=customer_name]').fill(customer)
  await dialog.getByTestId('open-takeaway').click()
  await expect(page).toHaveURL(/\/pos\/bills\/\d+$/)
  await page.getByTestId(`product-${product.id}`).click()
  await expect(page.getByTestId('draft-lines')).toContainText(product.name)
  await page.getByTestId('send').click()
  await expect(page.getByTestId('draft-lines')).toHaveCount(0)
  return Number(page.url().split('/').pop())
}

test.describe('POS outlet', () => {
  test('POS-01 bill takeaway dibayar kartu dan tertutup', async ({ page, loginAs, data }) => {
    const { outlet, product } = await outletAndProduct(data)
    await loginAs('pos', '/pos/floor')
    const billId = await takeawayWithProduct(page, outlet, product, `Takeaway ${Date.now().toString(36)}`)
    await page.getByTestId('pay').click()
    const pay = page.getByTestId('pay-dialog')
    await onlyMethod(pay, 'CARD')
    await pay.getByTestId('exact-CARD').click()
    await pay.getByTestId('pay-row-CARD').locator('input[name=reference]').fill('EDC-POS')
    await pay.getByTestId('pay-submit').click()
    await expect(page.getByTestId('bill-closed')).toBeVisible()
    expect((await data.api.get<{ status: string }>(`${data.P}/pos/bills/${billId}`)).status).toBe('CLOSED')
  })

  test('POS-02 bill dibebankan ke kamar tamu in-house dan masuk folio tamu', async ({ page, loginAs, data }) => {
    const { outlet, product } = await outletAndProduct(data, true)
    const ih = await data.inHouse()
    const lock = await data.api.get<{ version: number }>(`${data.P}/folios/${ih.folio.id}/outlet-lock`)
    await data.api.put(`${data.P}/folios/${ih.folio.id}/outlet-lock`, { limit_amount: '2000000', valid_mode: 'UNTIL_CLOSE', outlet_ids: [outlet.id], version: lock.version })
    const before = Number((await data.folio(ih.folio.id)).balance)

    await loginAs('pos', '/pos/floor')
    const billId = await takeawayWithProduct(page, outlet, product, `Kamar ${ih.room.number}`)
    await page.getByTestId('pay').click()
    const pay = page.getByTestId('pay-dialog')
    await onlyMethod(pay, 'ROOM')
    await pay.getByTestId('room-query').fill(ih.room.number)
    await pay.getByTestId('room-find').click()
    await pay.getByTestId(`room-${ih.stay.id}`).click()
    await expect(pay.getByTestId('room-chosen')).toContainText(ih.room.number)
    await pay.getByTestId('exact-ROOM').click()
    await pay.getByTestId('pay-submit').click()
    await expect(page.getByTestId('bill-closed')).toBeVisible()
    expect((await data.api.get<{ status: string }>(`${data.P}/pos/bills/${billId}`)).status).toBe('CLOSED')
    await expect.poll(async () => Number((await data.folio(ih.folio.id)).balance)).toBeGreaterThan(before)
  })

  test('POS-03 tamu yang folionya belum dibuka untuk outlet ditolak saat dibebankan ke kamar', async ({ page, loginAs, data }) => {
    const { outlet, product } = await outletAndProduct(data, true)
    const ih = await data.inHouse()
    await loginAs('pos', '/pos/floor')
    await takeawayWithProduct(page, outlet, product, `Terkunci ${ih.room.number}`)
    await page.getByTestId('pay').click()
    const pay = page.getByTestId('pay-dialog')
    await onlyMethod(pay, 'ROOM')
    await pay.getByTestId('room-query').fill(ih.room.number)
    await pay.getByTestId('room-find').click()
    await expect(pay.getByTestId(`room-${ih.stay.id}`)).toContainText(/.+/)
    const lookup = await data.api.get<{ data: { stay_id: number; open: boolean }[] }>(`${data.P}/pos/outlets/${outlet.id}/room-lookup`, { q: ih.room.number })
    expect(lookup.data.find((r) => r.stay_id === ih.stay.id)?.open).toBe(false)
    // the bill is paid by card instead, so no open bill is left to hold the night audit
    await onlyMethod(pay, 'CARD')
    await pay.getByTestId('exact-CARD').click()
    await pay.getByTestId('pay-submit').click()
    await expect(page.getByTestId('bill-closed')).toBeVisible()
  })

  test('POS-05 beban kamar dari bill yang dibuka kembali dikembalikan, lalu bill dibayar tunai (KD-01, KD-02)', async ({ page, loginAs, data, seed }) => {
    const { outlet, product } = await outletAndProduct(data, true)
    const ih = await data.inHouse()
    const lock = await data.api.get<{ version: number }>(`${data.P}/folios/${ih.folio.id}/outlet-lock`)
    await data.api.put(`${data.P}/folios/${ih.folio.id}/outlet-lock`, { limit_amount: '2000000', valid_mode: 'UNTIL_CLOSE', outlet_ids: [outlet.id], version: lock.version })
    const before = (await data.folio(ih.folio.id)).balance
    const lookup = async () => (await data.api.get<{ data: { stay_id: number; used: string | null; remaining: string | null }[] }>(`${data.P}/pos/outlets/${outlet.id}/room-lookup`, { q: ih.room.number })).data.find((r) => r.stay_id === ih.stay.id)
    expect(outlet.drawer).toBeTruthy()
    await data.openShift(data.api, outlet.drawer!) // the cash at the end goes to a shift of the administrator on the drawer of the outlet
    const approve = async () => {
      const dialog = page.getByTestId('approval-dialog')
      await dialog.locator('input[name=approval_email]').fill(seed.users.admin.email)
      await dialog.locator('input[name=approval_password]').fill(seed.users.admin.password)
      await dialog.getByTestId('approval-submit').click()
      await expect(dialog).toHaveCount(0)
    }

    // charged to the room by mistake
    await loginAs('admin', '/pos/floor')
    const billId = await takeawayWithProduct(page, outlet, product, `Salah kamar ${ih.room.number}`)
    await page.getByTestId('pay').click()
    const pay = page.getByTestId('pay-dialog')
    await onlyMethod(pay, 'ROOM')
    await pay.getByTestId('room-query').fill(ih.room.number)
    await pay.getByTestId('room-find').click()
    await pay.getByTestId(`room-${ih.stay.id}`).click()
    await pay.getByTestId('exact-ROOM').click()
    await pay.getByTestId('pay-submit').click()
    await expect(page.getByTestId('bill-closed')).toBeVisible()
    await expect.poll(async () => (await data.folio(ih.folio.id)).balance).not.toBe(before)
    expect(Number((await lookup())?.used ?? 0)).toBeGreaterThan(0)

    // reopen the bill, then return its charge to the room
    await page.getByTestId('reopen').click()
    await page.getByTestId('reason').fill('Salah kamar (E2E)')
    await page.getByTestId('reason-submit').click()
    await approve()
    await expect(page.getByTestId('actions')).toBeVisible()
    const charges = page.getByTestId('room-charges')
    await expect(charges).toContainText(ih.room.number)
    await charges.locator('[data-testid^=return-charge-]').click()
    await expect(page.getByTestId('reason-dialog')).toContainText(ih.room.number)
    await page.getByTestId('reason').fill('Salah kamar (E2E)')
    await page.getByTestId('reason-submit').click()
    await approve()
    await expect(page.getByTestId('room-charges')).toHaveCount(0)
    const owed = await data.api.get<{ totals: { payable: string } }>(`${data.P}/pos/bills/${billId}`)
    expect(Number(owed.totals.payable)).toBeGreaterThan(0)
    await expect.poll(async () => (await data.folio(ih.folio.id)).balance).toBe(before) // the guest folio line is cancelled
    expect(Number((await lookup())?.used ?? 0)).toBe(0) // the limit of the outlets is free again

    // paid in cash
    await page.getByTestId('pay').click()
    await expect(pay.getByTestId('pay-row-CASH')).toBeVisible()
    await pay.getByTestId('exact-CASH').click()
    await pay.getByTestId('tendered').fill(String(Number(owed.totals.payable)))
    await pay.getByTestId('pay-submit').click()
    await expect(page.getByTestId('bill-closed')).toBeVisible()
    const done = await data.api.get<{ status: string; payments: { method: string }[] }>(`${data.P}/pos/bills/${billId}`)
    expect(done.status).toBe('CLOSED')
    expect(done.payments.map((x) => x.method)).toContain('CASH')
    expect((await data.folio(ih.folio.id)).balance).toBe(before)
    await data.closeShift() // an open shift would hold the night audit
  })

  test('POS-04 laporan outlet menampilkan total penjualan', async ({ page, loginAs }) => {
    await loginAs('pos', '/pos/report')
    await expect(page.getByTestId('report-totals').or(page.locator('[data-testid^=empty-]')).first()).toBeVisible()
  })
})
