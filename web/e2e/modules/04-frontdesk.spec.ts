import { test, expect } from '../support/fixtures'
import { addDays } from '../support/factories'
import { tx } from '../support/i18n'
import { pickCombo, pickSelect, setDate } from '../support/ui'

/** The front desk: today's arrivals, walk-in, in house, room move, extension, reverse check-in, and the check-out with its payment step. */
test.describe('Front office', () => {
  test('FD-01 check-in tamu dari daftar kedatangan hari ini', async ({ page, loginAs, data }) => {
    const res = await data.reservation({ nights: 2 })
    const line = res.rooms[0]!
    await loginAs('frontdesk', `/arrivals?q=${encodeURIComponent(res.confirmation_number)}`)
    await page.getByTestId(`open-${line.id}`).click()
    const sheet = page.getByTestId('checkin-sheet')
    await expect(sheet).toBeVisible()
    // guest → room → deposit → done; the room assigned to the line is proposed first
    await expect(sheet.getByTestId('step-guest')).toBeVisible()
    await sheet.getByTestId('checkin-next').click()
    await expect(sheet.getByTestId('step-room')).toBeVisible()
    await expect(sheet.locator('select[name=room]')).toHaveValue(String(res.room!.id))
    await sheet.getByTestId('checkin-next').click()
    // the deposit step proposes the estimate in cash, which needs a cashier shift: the guest pays the deposit by card
    await expect(sheet.getByTestId('step-deposit')).toBeVisible()
    await sheet.getByTestId('deposit-method-CARD').click()
    await sheet.locator('input[name=deposit_reference]').fill('EDC-DEP')
    await expect(sheet.getByTestId('checkin-submit')).toBeEnabled()
    await sheet.getByTestId('checkin-submit').click()
    await expect(sheet.getByTestId('step-done')).toBeVisible()
    await expect(sheet.getByTestId('done-summary')).toContainText(res.room!.number)
    const inHouse = await data.api.get<{ data: { id: number; reservation_id: number; room: { number: string } }[] }>(`${data.P}/stays/in-house`, { limit: 200 })
    const stay = inHouse.data.find((s) => s.reservation_id === res.id)
    expect(stay?.room.number).toBe(res.room!.number)
    const detail = await data.stay(stay!.id)
    expect(Number((await data.folio(detail.folios[0]!.id)).balance)).toBeLessThan(0) // the deposit is a credit on the folio
  })

  test('FD-02 kamar kotor tidak bisa dipakai check-in tanpa izin override', async ({ page, loginAs, data }) => {
    const res = await data.reservation({ nights: 1 })
    await data.makeDirty(res.room!.id)
    const line = res.rooms[0]!
    await loginAs('frontdesk', `/arrivals?q=${encodeURIComponent(res.confirmation_number)}`)
    await page.getByTestId(`open-${line.id}`).click()
    const sheet = page.getByTestId('checkin-sheet')
    await sheet.getByTestId('checkin-next').click() // guest → room
    await expect(sheet.getByTestId('step-room')).toBeVisible()
    await sheet.locator('select[name=room]').selectOption(String(res.room!.id))
    await expect(sheet.getByTestId('not-ready')).toContainText(res.room!.number)
    await expect(sheet.getByTestId('checkin-next')).toBeDisabled()
    await expect(sheet.locator('input[name=override]')).toHaveCount(0) // the desk has no frontdesk.checkin_unready_room
  })

  test('FD-03 walk-in tamu baru tanpa reservasi', async ({ page, loginAs, data, seed }) => {
    const bd = await data.businessDate()
    const typeId = await data.roomTypeWithSpace(bd, addDays(bd, 1))
    const type = seed.roomTypes.find((t) => t.id === typeId)!
    const room = await data.freeRoom(typeId, bd, addDays(bd, 1))
    const lastName = `Walkin ${Date.now().toString(36)}`
    await loginAs('frontdesk', '/walk-in')
    const form = page.getByTestId('walkin-form')
    await pickSelect(form, 'room_type', type.code)
    await pickCombo(form, 'room', `${room.number} ·`)
    await setDate(form, 'departure', addDays(bd, 1))
    await form.locator('input[name=first_name]').fill('Wayan')
    await form.locator('input[name=last_name]').fill(lastName)
    await form.getByTestId('walkin-submit').click()
    await expect(page).toHaveURL(/\/stays\/\d+$/)
    await expect(page.getByTestId('guest-name')).toContainText(lastName)
    await expect(page.getByTestId('segments')).toContainText(room.number)
  })

  test('FD-04 tamu in-house tampil di daftar dan membuka detail stay', async ({ page, loginAs, data }) => {
    const ih = await data.inHouse()
    await loginAs('frontdesk', '/in-house')
    const row = page.getByRole('row').filter({ hasText: ih.guest.last_name })
    await expect(row.first()).toBeVisible()
    await row.first().getByRole('link').first().click()
    await expect(page).toHaveURL(new RegExp(`/stays/${ih.stay.id}`))
    await expect(page.getByTestId('stay-status')).toBeVisible()
  })

  test('FD-05 pindah kamar dengan alasan', async ({ page, loginAs, data }) => {
    const ih = await data.inHouse({ nights: 2 })
    const st = await data.stay(ih.stay.id)
    const bd = await data.businessDate()
    const target = await data.freeRoom(ih.reservation.rooms[0]!.room_type_id, bd, st.stay.departure_date)
    await loginAs('frontdesk', `/stays/${ih.stay.id}`)
    await page.getByTestId('open-move').click()
    const form = page.getByTestId('move-form')
    await pickCombo(form, 'room', `${target.number} ·`)
    await form.locator('input[name=reason]').fill('AC tidak dingin (E2E)')
    await form.getByRole('button', { name: tx('stayActions.move'), exact: true }).click()
    await expect.poll(async () => (await data.stay(ih.stay.id)).segments.map((s) => s.room_number)).toContain(target.number)
    await expect(page.getByTestId('segments')).toContainText(target.number)
  })

  test('FD-06 perpanjang menginap satu malam', async ({ page, loginAs, data }) => {
    const ih = await data.inHouse({ nights: 1 })
    const before = (await data.stay(ih.stay.id)).stay.departure_date
    await loginAs('frontdesk', `/stays/${ih.stay.id}`)
    await page.getByTestId('open-departure').click()
    const form = page.getByTestId('departure-form')
    await setDate(form, 'departure', addDays(before, 1))
    await form.getByRole('button', { name: tx('common.save'), exact: true }).click()
    await expect.poll(async () => (await data.stay(ih.stay.id)).stay.departure_date).toBe(addDays(before, 1))
  })

  test('FD-07 batalkan check-in (reverse) mengembalikan reservasi ke kedatangan', async ({ page, loginAs, data }) => {
    const ih = await data.inHouse()
    await loginAs('admin', `/stays/${ih.stay.id}`)
    await page.getByTestId('reverse').click()
    const form = page.getByTestId('reverse-form')
    await form.locator('input[name=reason]').fill('Salah kamar saat check-in (E2E)')
    await form.getByRole('button', { name: tx('stay.reverse') }).click()
    await expect.poll(async () => (await data.stay(ih.stay.id)).stay.status).not.toBe('OPEN')
    const res = await data.api.get<{ rooms: { status: string }[] }>(`${data.P}/reservations/${ih.reservation.id}`)
    expect(res.rooms[0]?.status).toBe('CONFIRMED')
  })

  test('FD-08 check-out dengan saldo: bayar di langkah check-out lalu selesai', async ({ page, loginAs, data }) => {
    const ih = await data.inHouse({ nights: 1 })
    await data.postCharge(ih.folio.id, 'FOOD_BEVERAGE', '150000', 'Minibar (E2E)')
    // Leaving on the day of arrival: the check-out posts tonight's room charge and then needs a zero balance, so a balance paid in the
    // wizard alone is refused (FOLIO_NOT_BALANCED). The desk posts tonight's charge first (the Room charges page does the same).
    await data.api.post(`${data.P}/night-audit/room-charges`, { business_date: await data.businessDate(), stay_ids: [ih.stay.id] })
    await loginAs('frontdesk', `/stays/${ih.stay.id}`)
    await page.getByTestId('checkout').click()
    const wizard = page.getByTestId('checkout-wizard')
    await expect(wizard.getByTestId('step-departure')).toBeVisible()
    await wizard.locator('input[name=confirm_early]').check() // the stay leaves before its departure date
    const pay = wizard.getByTestId('checkout-pay').first()
    await expect(pay).toBeVisible()
    await pay.locator('select[name=pay_method]').selectOption('CARD')
    await pay.locator('input[name=pay_reference]').fill('EDC-E2E')
    await pay.getByTestId('checkout-pay-submit').click()
    await expect(wizard.getByTestId('checkout-paid')).toBeVisible()
    await wizard.getByTestId('checkout-submit').click()
    await expect(wizard.getByTestId('checkout-done')).toBeVisible()
    expect((await data.stay(ih.stay.id)).stay.status).toBe('CHECKED_OUT')
    expect(Number((await data.folio(ih.folio.id)).balance)).toBe(0)
  })

  test('FD-09 daftar keberangkatan menampilkan tamu yang pulang hari ini', async ({ page, loginAs, data }) => {
    const bd = await data.businessDate()
    const ih = await data.inHouse({ nights: 1 })
    await loginAs('frontdesk', '/departures')
    const filters = page.getByTestId('departure-filters')
    await setDate(filters, 'departure_date', addDays(bd, 1))
    await expect(page.getByRole('row').filter({ hasText: ih.stay.stay_number })).toBeVisible()
  })
})
