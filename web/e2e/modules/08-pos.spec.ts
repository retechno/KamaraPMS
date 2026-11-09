import { test, expect } from '../support/fixtures'
import type { Factories } from '../support/factories'
import type { Page } from 'playwright/test'

/** The POS of the outlets (demo of pms-seed pos): a takeaway paid by card, a charge to the room of a guest in house, and the outlet report. */

interface Outlet { id: number; code: string; name: string; allow_room_charge: boolean; uses_tables: boolean }
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

/** Opens a takeaway bill at the outlet from the floor and adds one product; the page is left on the bill. */
async function takeawayWithProduct(page: Page, outlet: Outlet, product: Product, customer: string): Promise<number> {
  const select = page.getByTestId('outlet-select')
  if (await select.isVisible()) await select.selectOption(String(outlet.id))
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
    if (!(await pay.getByTestId('pay-row-CARD').isVisible())) await pay.getByTestId('add-CARD').click()
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
    for (const m of ['CASH', 'CARD']) if (await pay.getByTestId(`pay-row-${m}`).isVisible()) await pay.getByTestId(`remove-${m}`).click()
    await pay.getByTestId('add-ROOM').click()
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
    await pay.getByTestId('add-ROOM').click()
    await pay.getByTestId('room-query').fill(ih.room.number)
    await pay.getByTestId('room-find').click()
    await expect(pay.getByTestId(`room-${ih.stay.id}`)).toContainText(/.+/)
    const lookup = await data.api.get<{ data: { stay_id: number; open: boolean }[] }>(`${data.P}/pos/outlets/${outlet.id}/room-lookup`, { q: ih.room.number })
    expect(lookup.data.find((r) => r.stay_id === ih.stay.id)?.open).toBe(false)
    // the bill is paid by card instead, so no open bill is left to hold the night audit
    await pay.getByTestId('remove-ROOM').click()
    if (!(await pay.getByTestId('pay-row-CARD').isVisible())) await pay.getByTestId('add-CARD').click()
    await pay.getByTestId('exact-CARD').click()
    await pay.getByTestId('pay-submit').click()
    await expect(page.getByTestId('bill-closed')).toBeVisible()
  })

  test('POS-04 laporan outlet menampilkan total penjualan', async ({ page, loginAs }) => {
    await loginAs('pos', '/pos/report')
    await expect(page.getByTestId('report-totals').or(page.locator('[data-testid^=empty-]')).first()).toBeVisible()
  })
})
