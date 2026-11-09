import { test, expect } from '../support/fixtures'
import { addDays } from '../support/factories'

/**
 * The night audit closes the business day, so it runs last (the project "final", the teardown of setup) and alone.
 * What the module tests left behind is real work for it: arrivals that never checked in become no-shows through the screen,
 * the guests in house get their room night, and the business date moves on by one day.
 * E2E_SKIP_NIGHT_AUDIT=1 leaves the day open (to run one spec again and again on the same day).
 */
test.describe.configure({ mode: 'serial' })

test.describe('Night audit', () => {
  test.skip(!!process.env.E2E_SKIP_NIGHT_AUDIT, 'E2E_SKIP_NIGHT_AUDIT is set')

  test('NA-01 menandai no-show, menjalankan night audit, dan tanggal bisnis maju satu hari', async ({ page, loginAs, data }) => {
    test.setTimeout(180_000)
    const bd = await data.businessDate()
    const ih = await data.inHouse({ nights: 2 }) // a guest in house tonight: the audit posts the night
    const noShow = await data.reservation({ nights: 1 }) // an arrival that never comes

    await loginAs('admin', '/night-audit')
    await expect(page.getByTestId('audit-date')).toBeVisible()

    // arrivals of the day that did not check in: mark them all no-show
    const arrivals = page.getByTestId('arrivals')
    if (await arrivals.locator(`[data-testid=arrival-${noShow.rooms[0]!.id}]`).isVisible()) {
      await arrivals.getByTestId('select-all').check()
      await page.getByTestId('noshow-form').locator('input[name=reason]').fill('Tidak datang (E2E)')
      await page.getByTestId('noshow-form').locator('input[name=confirm]').check()
      await page.getByTestId('mark-no-shows').click()
      await expect(page.getByTestId('notice')).toBeVisible()
    }
    const line = await data.api.get<{ rooms: { status: string }[] }>(`${data.P}/reservations/${noShow.id}`)
    expect(line.rooms[0]?.status).toBe('NO_SHOW')

    // nothing else may hold the day: if something does, the reasons are the failure message
    const cannot = page.getByTestId('cannot-run')
    if (await cannot.isVisible()) throw new Error(`the night audit cannot run: ${await cannot.innerText()}`)

    await page.locator('input[name=confirm_run]').check()
    await page.getByTestId('run-audit').click()
    await expect(page.getByTestId('result')).toBeVisible({ timeout: 120_000 })
    await expect(page.getByTestId('new-date')).toBeVisible()
    expect(await data.businessDate()).toBe(addDays(bd, 1))

    // the night of the guest in house is on the folio
    const folio = await data.api.get<{ items: { charge_code?: string; service_date: string; transaction_type: string }[] }>(`${data.P}/folios/${ih.folio.id}`)
    expect(folio.items.some((i) => i.transaction_type === 'CHARGE' && i.service_date === bd)).toBe(true)
  })

  test('NA-02 arsip paket night audit memuat hari yang baru ditutup', async ({ page, loginAs, data }) => {
    const closed = addDays(await data.businessDate(), -1)
    await loginAs('admin', '/night-audit/archive')
    await expect(page.getByTestId(`pack-row-${closed}`)).toBeVisible({ timeout: 60_000 })
  })

  test('NA-03 transaksi bertanggal hari yang sudah ditutup ditolak (BUSINESS_DAY_CLOSED)', async ({ data }) => {
    const ih = await data.inHouse({ nights: 1 })
    const cc = await data.chargeCode('FOOD_BEVERAGE')
    const closed = addDays(await data.businessDate(), -1)
    const err = await data.api.post(`${data.P}/folios/${ih.folio.id}/charges`, { charge_code_id: cc.id, quantity: '1', unit_price: '10000', service_date: closed }).catch((e) => e)
    expect(err.status).toBeGreaterThanOrEqual(400)
  })
})
