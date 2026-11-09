import { test, expect } from '../support/fixtures'
import { tx } from '../support/i18n'
import { pickCombo } from '../support/ui'

/** The guest folio (charges, payments, corrections with approval) and the cashier shift (open, cash, close). */
test.describe('Folio dan kasir', () => {
  test('FOL-01 posting charge ke folio tamu in-house', async ({ page, loginAs, data }) => {
    const ih = await data.inHouse()
    const fb = await data.chargeCode('FOOD_BEVERAGE')
    await loginAs('frontdesk', `/folios/${ih.folio.id}`)
    await page.getByTestId('tab-charge').click()
    const form = page.getByTestId('charge-form')
    await pickCombo(form, 'charge_code', `${fb.code} ·`)
    await form.locator('input[name=quantity]').fill('2')
    await form.locator('input[name=unit_price]').fill('45000')
    await form.locator('input[name=description]').fill('Nasi goreng (E2E)')
    await form.getByRole('button', { name: tx('folio.postCharge') }).click()
    await expect(page.getByTestId('items')).toContainText('Nasi goreng (E2E)')
    await expect.poll(async () => Number((await data.folio(ih.folio.id)).balance)).toBeGreaterThanOrEqual(90000)
  })

  test('FOL-02 menerima pembayaran kartu dan saldo berkurang', async ({ page, loginAs, data }) => {
    const ih = await data.inHouse()
    await data.postCharge(ih.folio.id, 'FOOD_BEVERAGE', '200000', 'Makan malam (E2E)')
    const before = Number((await data.folio(ih.folio.id)).balance)
    await loginAs('frontdesk', `/folios/${ih.folio.id}?tab=payment`)
    const form = page.getByTestId('payment-form')
    await form.locator('input[name=amount]').fill('100000')
    await form.locator('select[name=payment_method]').selectOption('CARD')
    await form.locator('input[name=reference]').fill('EDC-1234')
    await form.getByRole('button', { name: tx('folio.postPayment') }).click()
    await expect.poll(async () => Number((await data.folio(ih.folio.id)).balance)).toBe(before - 100000)
    await expect(page.getByTestId('balance')).toBeVisible()
  })

  test('FOL-03 pembayaran tunai tanpa shift kasir diperingatkan', async ({ page, loginAs, data }) => {
    const ih = await data.inHouse()
    await loginAs('frontdesk', `/folios/${ih.folio.id}?tab=payment`)
    const form = page.getByTestId('payment-form')
    await form.locator('select[name=payment_method]').selectOption('CASH')
    const notice = page.getByTestId('shift-required')
    await expect(notice).toContainText(tx('shiftNotice.title'))
    await expect(notice.getByTestId('shift-open')).toBeVisible() // the desk may open a shift from here
  })

  test('FOL-04 koreksi (reverse) charge hari ini dengan persetujuan atasan', async ({ page, loginAs, data, seed }) => {
    const ih = await data.inHouse()
    const { item } = await data.postCharge(ih.folio.id, 'FOOD_BEVERAGE', '75000', 'Salah posting (E2E)')
    await loginAs('supervisor', `/folios/${ih.folio.id}`)
    await page.getByTestId(`reverse-${item.id}`).first().click()
    const form = page.getByTestId('correction-form')
    await form.locator('input[name=reason]').fill('Salah kamar (E2E)')
    await form.getByRole('button', { name: tx('folio.continueApproval') }).click()
    const dialog = page.getByTestId('approval-dialog')
    await dialog.locator('input[name=approval_email]').fill(seed.users.admin.email)
    await dialog.locator('input[name=approval_password]').fill(seed.users.admin.password)
    await dialog.getByTestId('approval-submit').click()
    await expect(dialog).toHaveCount(0)
    await expect.poll(async () => Number((await data.folio(ih.folio.id)).balance)).toBe(0)
  })

  test('FOL-05 daftar folio bisa dicari dengan nomor kamar', async ({ page, loginAs, data }) => {
    const ih = await data.inHouse()
    await loginAs('frontdesk', '/folios')
    await page.locator('input[name=q]').fill(ih.folio.folio_number)
    await page.locator('input[name=q]').press('Enter')
    await expect(page.getByTestId(`folio-${ih.folio.folio_number}`)).toBeVisible()
  })

  test('CSH-01 buka shift, terima tunai, tutup shift dengan hitungan yang cocok', async ({ page, loginAs, apiAs, data }) => {
    const cashier = await apiAs('cashier')
    await data.closeShift(cashier) // a shift left open by an earlier run
    const ih = await data.inHouse()
    await data.postCharge(ih.folio.id, 'FOOD_BEVERAGE', '250000', 'Spa (E2E)')
    const drawer = `LACI-${Date.now().toString(36).toUpperCase()}`

    await loginAs('cashier', '/cashier/shifts')
    const open = page.getByTestId('open-form')
    await open.locator('input[name=drawer]').fill(drawer)
    await open.locator('input[name=opening_float]').fill('500000')
    await open.getByTestId('open-shift').click()
    await expect(page.getByTestId('current-shift')).toBeVisible()

    await page.goto(`/folios/${ih.folio.id}?tab=payment`)
    const pay = page.getByTestId('payment-form')
    await pay.locator('input[name=amount]').fill('250000')
    await pay.locator('select[name=payment_method]').selectOption('CASH')
    await pay.getByRole('button', { name: tx('folio.postPayment') }).click()
    await expect.poll(async () => Number((await data.folio(ih.folio.id)).balance)).toBeLessThanOrEqual(0)

    const cur = await cashier.get<{ shift: { id: number; cash: { expected: string } } }>(`${data.P}/cashier/shifts/current`)
    expect(Number(cur.shift.cash.expected)).toBe(750000)
    await page.goto('/cashier/shifts')
    await page.getByTestId('start-close').click()
    const close = page.getByTestId('close-form')
    await close.locator('input[name=counted_cash]').fill(cur.shift.cash.expected)
    await close.getByTestId('confirm-close').click()
    await expect(page.getByTestId('closed-card')).toBeVisible()
    const after = await cashier.get<{ shift: unknown }>(`${data.P}/cashier/shifts/current`)
    expect(after.shift).toBeNull()
  })

  test('CSH-02 rekap kasir per metode pembayaran untuk tanggal bisnis', async ({ page, loginAs, data }) => {
    const ih = await data.inHouse()
    await data.pay(ih.folio.id, '120000', 'CARD')
    await loginAs('cashier', '/cashier')
    await page.getByRole('button', { name: tx('cashier.show') }).click()
    await expect(page.getByTestId('totals')).toBeVisible()
    await expect(page.getByTestId('total-CARD')).toBeVisible()
  })
})
