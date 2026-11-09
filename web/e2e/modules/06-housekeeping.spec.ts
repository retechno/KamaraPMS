import { test, expect } from '../support/fixtures'
import { addDays } from '../support/factories'
import { tx } from '../support/i18n'
import { pickCombo, setDate } from '../support/ui'

/** Housekeeping, maintenance, lost and found and room blocks: the work of the rooms. */
test.describe('Housekeeping dan kamar', () => {
  test('HK-01 kamar kotor dibersihkan lalu diinspeksi dari papan housekeeping', async ({ page, loginAs, data }) => {
    const bd = await data.businessDate()
    const typeId = await data.roomTypeWithSpace(bd, addDays(bd, 1))
    const room = await data.freeRoom(typeId, bd, addDays(bd, 1))
    await data.makeDirty(room.id)
    const status = async () => (await data.api.get<{ data: { room_id: number; status: string }[] }>(`${data.P}/housekeeping`)).data.find((r) => r.room_id === room.id)?.status

    await loginAs('housekeeping', '/housekeeping')
    await page.getByTestId('filter-DIRTY').click()
    const row = page.getByTestId(`room-${room.number}`)
    await expect(row).toBeVisible()
    await row.getByTestId('act-CLEANING').click()
    await expect.poll(status).toBe('CLEANING')
    await page.getByTestId('filter-DIRTY').click() // filter off again: the room is no longer dirty
    await row.getByTestId('act-CLEAN').click()
    await expect.poll(status).toBe('CLEAN')
    await row.getByTestId('act-INSPECTED').click()
    await expect.poll(status).toBe('INSPECTED')
  })

  test('HK-02 status board menampilkan setiap kamar dengan status housekeeping', async ({ page, loginAs, data }) => {
    const rooms = await data.api.all<{ room_number: string }>(`${data.P}/rooms`)
    await loginAs('frontdesk', '/room-status')
    await expect(page.getByTestId('counts')).toBeVisible()
    await expect(page.getByTestId(`tile-${rooms[0]!.room_number}`)).toBeVisible()
    await page.locator('input[name=room_search]').fill(rooms[0]!.room_number)
    await expect(page.locator('[data-testid^=tile-]')).toHaveCount(1)
  })

  test('HK-03 membuat daftar tugas pembersihan dan menyelesaikan satu tugas', async ({ page, loginAs, data }) => {
    const bd = await data.businessDate()
    const typeId = await data.roomTypeWithSpace(bd, addDays(bd, 1))
    const room = await data.freeRoom(typeId, bd, addDays(bd, 1))
    await data.makeDirty(room.id)
    await loginAs('admin', '/housekeeping/tasks')
    await page.getByTestId('generate').click()
    await expect(page.getByTestId('tasks')).toBeVisible()
    await page.getByTestId(`start-${room.number}`).first().click()
    await page.getByTestId(`done-${room.number}`).first().click()
    await expect.poll(async () => (await data.api.get<{ data: { room_id: number; status: string }[] }>(`${data.P}/housekeeping`)).data.find((r) => r.room_id === room.id)?.status).toMatch(/CLEAN|INSPECTED/)
  })

  test('MT-01 melaporkan kerusakan kamar lalu menyelesaikannya', async ({ page, loginAs, data }) => {
    const rooms = await data.api.all<{ id: number; room_number: string }>(`${data.P}/rooms`)
    const room = rooms[rooms.length - 1]!
    const description = `Keran bocor ${Date.now().toString(36)} (E2E)`
    await loginAs('frontdesk', '/maintenance')
    const newBtn = page.getByTestId('new-request')
    if (await newBtn.isVisible()) await newBtn.click()
    const form = page.getByTestId('request-form')
    await pickCombo(form, 'room_id', `${room.room_number} ·`)
    await form.locator('select[name=category]').selectOption('PLUMBING')
    await form.locator('select[name=priority]').selectOption('HIGH')
    await form.locator('input[name=description]').fill(description)
    await form.getByRole('button', { name: tx('maintenance.reportButton') }).click()

    const find = async () => (await data.api.get<{ data: { id: number; request_number: string; status: string; description: string }[] }>(`${data.P}/maintenance-requests`, { limit: 200 })).data.find((r) => r.description === description)
    await expect.poll(async () => (await find())?.status).toBe('OPEN')
    const req = (await find())!

    await page.goto('/maintenance')
    await page.getByTestId(`select-${req.request_number}`).click()
    const detail = page.getByTestId('detail')
    await detail.getByTestId('start').click()
    await expect.poll(async () => (await find())?.status).toBe('IN_PROGRESS')
    await detail.getByTestId('resolve').click()
    await page.getByTestId('close-form').locator('input[name=note]').fill('Seal keran diganti')
    await page.getByTestId('close-submit').click()
    await expect.poll(async () => (await find())?.status).toBe('RESOLVED')
  })

  test('LF-01 mencatat barang temuan lalu menyerahkannya ke pemilik', async ({ page, loginAs, data }) => {
    const description = `Kacamata hitam ${Date.now().toString(36)} (E2E)`
    await loginAs('frontdesk', '/lost-found')
    const newBtn = page.getByTestId('new-item')
    if (await newBtn.isVisible()) await newBtn.click()
    const form = page.getByTestId('item-form')
    await form.locator('input[name=description]').fill(description)
    await form.locator('select[name=category]').selectOption({ index: 1 })
    await form.locator('input[name=storage_location]').fill('Laci FO 1')
    await form.locator('input[name=possible_owner]').fill('Daniel Hartono')
    await form.getByRole('button', { name: tx('lostFound.recordButton') }).click()

    const find = async () => (await data.api.get<{ data: { item_number: string; status: string; description: string }[] }>(`${data.P}/lost-found`, { limit: 200 })).data.find((r) => r.description === description)
    await expect.poll(async () => (await find())?.status).toBeTruthy()
    const item = (await find())!
    await page.getByTestId(`select-${item.item_number}`).click()
    await page.getByTestId('detail').getByTestId('return').click()
    const ret = page.getByTestId('return-form')
    await expect(ret.locator('input[name=claimant_name]')).toHaveValue('Daniel Hartono')
    await ret.locator('input[name=claimant_proof]').fill('KTP 5171xxxx')
    await ret.getByTestId('return-submit').click()
    await expect.poll(async () => (await find())?.status).toBe('RETURNED')
  })

  test('BLK-01 memblok kamar out of order untuk perbaikan', async ({ page, loginAs, data }) => {
    const start = addDays(await data.businessDate(), 25)
    const typeId = await data.roomTypeWithSpace(start, addDays(start, 2))
    const room = await data.freeRoom(typeId, start, addDays(start, 2))
    await loginAs('admin', '/room-blocks')
    const form = page.getByTestId('block-form')
    await pickCombo(form, 'room_id', room.number)
    await form.locator('select[name=block_type]').selectOption('OOO')
    await setDate(form, 'start_date', start)
    await setDate(form, 'end_date', addDays(start, 2))
    await form.locator('input[name=reason]').fill('Ganti karpet (E2E)')
    await form.getByRole('button', { name: tx('roomBlocks.create') }).click()
    await expect.poll(async () => (await data.api.get<{ data: { room_id: number; start_date: string; status?: string }[] }>(`${data.P}/room-blocks`)).data.some((b) => b.room_id === room.id && b.start_date === start)).toBe(true)
    // the room is no longer offered for those nights
    const free = (await data.api.get<{ data: { room_id: number }[] }>(`${data.P}/availability/rooms`, { room_type_id: typeId, arrival: start, departure: addDays(start, 1) })).data
    expect(free.some((r) => r.room_id === room.id)).toBe(false)
  })
})
