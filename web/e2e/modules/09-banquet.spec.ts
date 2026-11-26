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

  test('BQ-04 pax aktual yang diketik saat posting sesi ditagih bila melebihi garansi (KD-03)', async ({ page, loginAs, data }) => {
    // a definite event today with lunch per guest (150,000) for 40 guaranteed: 42 came
    const P = `${data.P}/banquet`
    const bd = await data.businessDate()
    const company = await data.company()
    const rooms = await data.api.all<{ id: number }>(`${P}/rooms`)
    const items = await data.api.all<{ id: number; code: string }>(`${P}/items`)
    const tpls = await data.api.all<{ id: number; code: string }>(`${P}/policy-templates`)
    const ev = await data.api.post<{ id: number }>(`${P}/events`, { name: `Makan siang ${Date.now().toString(36)}`, booker_company_id: company.id, contact_name: 'Ibu Sari' })
    const withS = await data.api.post<{ sessions: { id: number }[] }>(`${P}/events/${ev.id}/sessions`, {
      name: 'Makan siang', room_id: rooms[rooms.length - 1]!.id, session_date: bd, start_time: '19:00', end_time: '22:00', pax_estimated: 45, pax_guaranteed: 40, accept_capacity: true,
    })
    const sid = withS.sessions[0]!.id
    await data.api.post(`${P}/events/${ev.id}/lines`, { session_id: sid, item_id: items.find((i) => i.code === 'LUNCH')!.id })
    await data.api.post(`${P}/events/${ev.id}/tentative`, { option_until: bd })
    await data.api.post(`${P}/events/${ev.id}/confirm`, { policy_template_id: tpls.find((t) => t.code === 'STANDARD')!.id })

    await loginAs('admin', `/banquet/events/${ev.id}?tab=folio`)
    await page.getByTestId(`folio-post-${sid}`).click()
    const d = page.getByTestId('post-dialog')
    await expect(d.getByTestId('post-billed')).toHaveText('40')
    await d.locator('input[name=pax_actual]').fill('42')
    await expect(d.getByTestId('post-billed')).toHaveText('42')
    await d.getByTestId('post-save').click()
    await expect(page.getByTestId(`folio-posted-${sid}`)).toBeVisible()

    const money = await data.api.get<{ postings: { session_id: number; pax_billed: number; pax_basis: string }[] }>(`${P}/events/${ev.id}/money`)
    expect(money.postings.find((x) => x.session_id === sid)).toMatchObject({ pax_billed: 42, pax_basis: 'ACTUAL' })
    const got = await data.api.get<{ sessions: { id: number; pax_actual: number | null }[] }>(`${P}/events/${ev.id}`)
    expect(got.sessions.find((x) => x.id === sid)?.pax_actual).toBe(42)
  })
})
