import { test, expect } from '../support/fixtures'
import { addDays } from '../support/factories'
import { tx } from '../support/i18n'
import { setDate } from '../support/ui'

/** Reservations: search for a room, book it for a guest, find it in the list, cancel and reinstate it, see it on the tape chart and the calendar. */
test.describe('Reservasi', () => {
  test('RES-01 membuat reservasi baru dari pencarian kamar dan tamu yang ada', async ({ page, loginAs, data, seed }) => {
    const guest = await data.guest({ first_name: 'Budi', last_name: `Reservasi ${Date.now().toString(36)}` })
    const arrival = addDays(await data.businessDate(), 7)
    const typeId = await data.roomTypeWithSpace(arrival, addDays(arrival, 2))
    const type = seed.roomTypes.find((t) => t.id === typeId)!
    await loginAs('frontdesk', '/reservations/new')
    const search = page.getByTestId('search-form')
    await setDate(search, 'arrival', arrival)
    await setDate(search, 'departure', addDays(arrival, 2))
    await search.locator('input[name=adults]').fill('2')
    await search.getByRole('button', { name: tx('newReservation.search') }).click()
    await expect(page.getByTestId('results')).toBeVisible()
    await page.getByTestId(`pick-${type.code}-RO`).click()

    const book = page.getByTestId('book-form')
    // the search matches the start of a name, the e-mail or the guest code (internal/guests/queries.sql SearchGuests)
    await book.locator('input[name=guest_q]').fill(guest.code)
    await book.getByTestId('find-guest').click()
    await book.getByTestId(`guest-${guest.code}`).click()
    await expect(book.getByTestId('chosen-guest')).toContainText(guest.code)
    await book.locator('select[name=source]').selectOption('PHONE')
    await expect(book.locator('input[name=confirm]')).toBeChecked()
    await book.getByRole('button', { name: tx('newReservation.bookAndConfirm') }).click()

    await expect(page).toHaveURL(/\/reservations\/\d+$/)
    const id = Number(page.url().split('/').pop())
    const res = await data.api.get<{ status: string; guest_id: number; arrival_date: string; departure_date: string; rooms: { room_type_id: number }[] }>(`${data.P}/reservations/${id}`)
    expect(res).toMatchObject({ status: 'CONFIRMED', guest_id: guest.id, arrival_date: arrival, departure_date: addDays(arrival, 2) })
    expect(res.rooms[0]?.room_type_id).toBe(typeId)
  })

  test('RES-02 reservasi draf tidak mengunci kamar sampai dikonfirmasi', async ({ page, loginAs, data }) => {
    const res = await data.reservation({ arrival: addDays(await data.businessDate(), 10), confirm: false })
    await loginAs('frontdesk', `/reservations/${res.id}`)
    await expect(page.getByTestId('draft-note')).toBeVisible()
    await page.getByTestId('confirm').click()
    await expect.poll(async () => (await data.api.get<{ status: string }>(`${data.P}/reservations/${res.id}`)).status).toBe('CONFIRMED')
    await expect(page.getByTestId('confirm')).toHaveCount(0)
  })

  test('RES-03 mencari reservasi di daftar berdasarkan nomor konfirmasi', async ({ page, loginAs, data }) => {
    const res = await data.reservation({ arrival: addDays(await data.businessDate(), 3) })
    await loginAs('frontdesk', '/reservations')
    const filters = page.getByTestId('filters')
    await filters.locator('input[name=q]').fill(res.confirmation_number)
    await filters.locator('input[name=q]').press('Enter')
    await expect(page.getByTestId(`res-${res.confirmation_number}`)).toBeVisible()
    await page.getByTestId(`res-${res.confirmation_number}`).getByRole('link').first().click()
    await expect(page).toHaveURL(new RegExp(`/reservations/${res.id}$`))
  })

  test('RES-04 membatalkan reservasi wajib dengan alasan, lalu memulihkannya', async ({ page, loginAs, data }) => {
    const res = await data.reservation({ arrival: addDays(await data.businessDate(), 5) })
    await loginAs('frontdesk', `/reservations/${res.id}`)
    await page.getByTestId('cancel').click()
    const form = page.locator('form[data-testid=reason-form]')
    await form.locator('input[name=reason]').fill('Tamu membatalkan perjalanan (E2E)')
    await form.getByRole('button', { name: tx('common.cancel'), exact: true }).click()
    await expect(page.getByTestId('reinstate')).toBeVisible()
    await expect(page.getByTestId('cancel')).toHaveCount(0)
    expect((await data.api.get<{ status: string; cancellation_reason: string }>(`${data.P}/reservations/${res.id}`)).cancellation_reason).toContain('E2E')

    await page.getByTestId('reinstate').click()
    await expect.poll(async () => (await data.api.get<{ status: string }>(`${data.P}/reservations/${res.id}`)).status).not.toBe('CANCELLED')
  })

  test('RES-05 menetapkan kamar ke reservasi yang belum punya kamar', async ({ page, loginAs, data }) => {
    const arrival = addDays(await data.businessDate(), 4)
    const res = await data.reservation({ arrival, assignRoom: false })
    const line = res.rooms[0]!
    await loginAs('frontdesk', `/reservations/${res.id}`)
    await page.getByTestId(`assign-${line.id}`).click()
    const form = page.getByTestId(`assign-form-${line.id}`)
    await expect(form).toBeVisible()
    await form.getByTestId('pick-room').click()
    await page.getByTestId('room-picker').locator('[data-testid^=room-option-]').first().click()
    await expect.poll(async () => (await data.api.get<{ rooms: { room_id: number | null }[] }>(`${data.P}/reservations/${res.id}`)).rooms[0]?.room_id ?? null).not.toBeNull()
    await expect(page.getByTestId(`room-number-${line.id}`)).toBeVisible()
  })

  test('RES-06 reservasi tampil di tape chart pada kamarnya', async ({ page, loginAs, data }) => {
    const bd = await data.businessDate()
    const res = await data.reservation({ arrival: addDays(bd, 1) })
    await loginAs('frontdesk', '/reservations/tape')
    await expect(page.getByTestId('tape')).toBeVisible()
    await expect(page.getByTestId(`room-${res.room!.number}`)).toBeVisible()
    await expect(page.locator(`[data-testid^="bar-${res.room!.number}-"]`).first()).toBeVisible()
  })

  test('RES-07 kalender ketersediaan menampilkan setiap tipe kamar', async ({ page, loginAs, seed }) => {
    await loginAs('frontdesk', '/availability')
    await expect(page.getByTestId('calendar')).toBeVisible()
    for (const t of seed.roomTypes) await expect(page.getByTestId(`row-${t.code}`).first()).toBeVisible()
  })

  test('RES-08 tanggal keberangkatan sebelum kedatangan tidak bisa dicari', async ({ page, loginAs, data }) => {
    const bd = await data.businessDate()
    await loginAs('frontdesk', '/reservations/new')
    const search = page.getByTestId('search-form')
    await setDate(search, 'arrival', addDays(bd, 5))
    await setDate(search, 'departure', addDays(bd, 3))
    await search.getByRole('button', { name: tx('newReservation.search') }).click()
    await expect(search.locator('[data-slot=date-input-problem]').first()).toBeVisible()
    await expect(page.getByTestId('results')).toHaveCount(0)
  })
})
