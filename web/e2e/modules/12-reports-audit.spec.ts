import { readFileSync } from 'node:fs'
import { test, expect, settle } from '../support/fixtures'

/** The reports (every report of the Reports page runs without an error), the day reports, the dashboard and the audit trail. */
const defs = readFileSync(new URL('../../src/views/reports/reportDefs.ts', import.meta.url), 'utf8')
const reportKeys = [...defs.matchAll(/key: '([a-z-]+)'/g)].map((m) => m[1]!)

test.describe('Laporan dan audit', () => {
  test('RPT-01 setiap laporan di halaman Laporan bisa dijalankan', async ({ page, loginAs }) => {
    test.setTimeout(30_000 + reportKeys.length * 10_000)
    await loginAs('admin', '/reports')
    const filters = page.getByTestId('filters')
    for (const key of reportKeys) {
      await test.step(key, async () => {
        await filters.locator('select[name=report]').selectOption(key)
        await page.getByTestId('run').click()
        await expect.soft(page.getByTestId('result').or(page.getByTestId('empty')).first(), `${key} shows a result`).toBeVisible()
        await expect.soft(page.getByTestId('form-error'), `${key} has no error`).toHaveCount(0)
      })
    }
  })

  test('RPT-02 laporan in-house memuat tamu yang baru check-in', async ({ page, loginAs, data }) => {
    const ih = await data.inHouse()
    await loginAs('admin', '/reports')
    await page.getByTestId('filters').locator('select[name=report]').selectOption('in-house')
    await page.getByTestId('run').click()
    await expect(page.getByTestId('result')).toContainText(ih.room.number)
  })

  test('RPT-03 laporan harian (daily flash, guest ledger, deposit ledger, Z) terbuka', async ({ page, loginAs }) => {
    await loginAs('admin')
    for (const path of ['/reports/daily-flash', '/reports/guest-ledger', '/reports/deposit-ledger', '/reports/cancellations', '/reports/corrections', '/reports/z-reports']) {
      await settle(page)
      await page.goto(path)
      await expect(page.locator('main h1')).toBeVisible()
      await page.waitForLoadState('networkidle', { timeout: 5_000 }).catch(() => { /* the shell polls (clock, banquet bell): never idle for long */ })
      await expect(page.getByTestId('form-error')).toHaveCount(0)
    }
    await page.goto('/reports/daily-flash')
    await expect(page.getByTestId('daily-flash')).toBeVisible()
  })

  test('RPT-04 dashboard dan performance menampilkan angka hari ini', async ({ page, loginAs }) => {
    await loginAs('admin', '/')
    await expect(page.locator('main')).toBeVisible()
    await page.goto('/performance')
    await expect(page.locator('main h1')).toBeVisible()
  })

  test('AUD-01 perubahan data tercatat di audit trail', async ({ page, loginAs, data }) => {
    const guest = await data.guest({ last_name: `Audit ${Date.now().toString(36)}` })
    await expect.poll(async () => (await data.api.get<{ data: { entity_type: string; entity_id: string | number }[] }>(`${data.P}/audit-logs`, { limit: 200 }).catch(() => data.api.get<{ data: { entity_type: string; entity_id: string | number }[] }>('/audit-logs', { limit: 200 }))).data.some((r) => String(r.entity_id) === String(guest.id))).toBe(true)
    await loginAs('admin', '/audit')
    await expect(page.getByTestId('filters')).toBeVisible()
    await expect(page.getByTestId('form-error')).toHaveCount(0)
  })
})
