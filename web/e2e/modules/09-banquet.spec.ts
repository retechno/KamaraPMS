import { test, expect } from '../support/fixtures'
import { addDays } from '../support/factories'
import { setDate } from '../support/ui'

/** Banquet (demo of pms-seed banquet): an inquiry for a company, a session in a meeting room, and the tentative hold with its option date. */
test.describe('Banquet', () => {
  test('BQ-01 membuat event inquiry untuk perusahaan dan menambah sesi di ruang meeting', async ({ page, loginAs, data }) => {
    const company = await data.company()
    const date = addDays(await data.businessDate(), 21)
    const name = `Rapat Tahunan ${Date.now().toString(36)}`
    await loginAs('admin', '/banquet/events')
    await page.getByTestId('event-new').click()
    const dialog = page.getByTestId('banquet-event-dialog')
    await dialog.locator('input[name=name]').fill(name)
    const types = dialog.locator('select[name=event_type_id] option')
    if ((await types.count()) > 1) await dialog.locator('select[name=event_type_id]').selectOption({ index: 1 })
    await dialog.locator('select[name=booker]').selectOption('COMPANY')
    await dialog.locator('select[name=booker_company_id]').selectOption(String(company.id))
    await dialog.locator('input[name=contact_name]').fill('Ibu Ratna')
    await dialog.getByTestId('banquet-event-save').click()
    await expect(page).toHaveURL(/\/banquet\/events\/\d+$/)
    const eventId = Number(page.url().split('/').pop())
    await expect(page.getByTestId('event-status')).toBeVisible()

    await page.getByTestId('tab-sessions').click()
    await page.getByTestId('session-add').click()
    const session = page.getByTestId('banquet-session-dialog')
    await session.locator('input[name=name]').fill('Sesi pleno')
    await session.getByTestId('session-room').selectOption({ index: 1 })
    await setDate(session, 'session_date', date)
    await session.locator('input[name=start_time]').fill('09:00')
    await session.locator('input[name=end_time]').fill('17:00')
    await session.locator('input[name=pax_estimated]').fill('12')
    await session.locator('input[name=pax_guaranteed]').fill('10')
    await session.getByTestId('banquet-session-save').click()
    await expect(page.getByTestId('session-list').locator('[data-testid^=session-]').first()).toBeVisible()

    const ev = await data.api.get<{ status: string; booker_company_id: number; name: string }>(`${data.P}/banquet/events/${eventId}`)
    expect(ev).toMatchObject({ status: 'INQUIRY', booker_company_id: company.id, name })
  })

  test('BQ-02 event dijadikan tentative dengan batas opsi', async ({ page, loginAs, data }) => {
    const company = await data.company()
    const bd = await data.businessDate()
    const ev = await data.api.post<{ id: number }>(`${data.P}/banquet/events`, { name: `Seminar ${Date.now().toString(36)}`, booker_company_id: company.id, contact_name: 'Pak Made' })
    const rooms = (await data.api.get<{ data: { id: number }[] }>(`${data.P}/banquet/rooms`)).data
    await data.api.post(`${data.P}/banquet/events/${ev.id}/sessions`, { room_id: rooms[0]!.id, session_date: addDays(bd, 30), start_time: '08:00', end_time: '12:00', pax_estimated: 12, pax_guaranteed: 10, name: 'Sesi pagi', accept_capacity: true })
    await loginAs('admin', `/banquet/events/${ev.id}`)
    await page.getByTestId('event-tentative').click()
    const dialog = page.getByTestId('tentative-dialog')
    await setDate(dialog, 'option_until', addDays(bd, 7))
    await dialog.getByTestId('tentative-save').click()
    await expect.poll(async () => (await data.api.get<{ status: string }>(`${data.P}/banquet/events/${ev.id}`)).status).toBe('TENTATIVE')
    await expect(page.getByTestId('option-until')).toBeVisible()
  })

  test('BQ-03 kalender banquet menampilkan ruang meeting hasil seed', async ({ page, loginAs, data }) => {
    const rooms = (await data.api.get<{ data: { code: string }[] }>(`${data.P}/banquet/rooms`)).data
    await loginAs('admin', '/banquet')
    await expect(page.getByTestId('calendar-day').or(page.getByTestId('calendar-week')).first()).toBeVisible()
    await expect(page.getByTestId(`room-head-${rooms[0]!.code}`).first()).toBeVisible()
  })
})
