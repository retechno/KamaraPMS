import { test } from '../support/fixtures'
import { buildWorld, readWorld } from './world'
import * as flows from './flows'

/**
 * The screenshots of the user guide (docs/user-guide). Each test runs one flow of flows.ts: the steps of one page of the guide, with the picture
 * of its key steps (at most three per page) written to docs/user-guide/screenshots under the same names every run.
 *
 *   E2E_DOCS=1 E2E_SKIP_NIGHT_AUDIT=1 scripts/e2e.sh docs-screenshots.spec.ts
 *
 * The tests run in order in one worker: "dunia" builds the hotel of the pictures first (world.ts), the others read it. A failed test does not
 * stop the next ones; its pictures stay those of the last good run.
 */
test.describe.configure({ mode: 'default' })

test.describe('Panduan pengguna: screenshot', () => {
  test('dunia: hotel contoh untuk gambar panduan', async ({ admin, seed }) => {
    test.setTimeout(300_000)
    await buildWorld(admin, seed)
  })

  // --- 00-memulai
  test('00 login dan navigasi', async ({ page, seed }) => { await flows.flowLogin(page, seed) })
  test('00 mengganti bahasa dan tampilan', async ({ page, loginAs }) => { await flows.flowBahasaTampilan(page, loginAs) })
  test('00 setup awal: pajak dan kode biaya', async ({ page, loginAs }) => { await flows.flowSetupPajak(page, loginAs) })
  test('00 memahami tanggal bisnis', async ({ page, loginAs }) => { await flows.flowTanggalBisnis(page, loginAs) })
  test('00 peran dan hak akses', async ({ page, loginAs }) => { await flows.flowPeran(page, loginAs) })

  // --- 01-front-desk
  test('01 membuat reservasi', async ({ page, loginAs, seed }) => { await flows.flowReservasiBaru(page, loginAs, readWorld(), seed) })
  test('01 mengonfirmasi dan mengubah reservasi', async ({ page, loginAs }) => { await flows.flowKonfirmasiReservasi(page, loginAs, readWorld()) })
  test('01 menetapkan kamar', async ({ page, loginAs }) => { await flows.flowTetapkanKamar(page, loginAs, readWorld()) })
  test('01 menerima deposit', async ({ page, loginAs }) => { await flows.flowDeposit(page, loginAs, readWorld()) })
  test('01 membatalkan reservasi', async ({ page, loginAs }) => { await flows.flowBatalReservasi(page, loginAs, readWorld()) })
  test('01 check-in', async ({ page, loginAs }) => { await flows.flowCheckIn(page, loginAs, readWorld()) })
  test('01 walk-in', async ({ page, loginAs, seed }) => { await flows.flowWalkIn(page, loginAs, seed) })
  test('01 pindah kamar', async ({ page, loginAs, seed }) => { await flows.flowPindahKamar(page, loginAs, readWorld(), seed) })
  test('01 perpanjang atau persingkat', async ({ page, loginAs }) => { await flows.flowPerpanjang(page, loginAs, readWorld()) })
  test('01 mengubah tarif tamu menginap (dan 00 meminta persetujuan)', async ({ page, loginAs }) => { await flows.flowUbahTarif(page, loginAs, readWorld()) })
  test('01 check-out', async ({ page, loginAs }) => { await flows.flowCheckOut(page, loginAs, readWorld()) })
  test('01 membatalkan check-in', async ({ page, loginAs }) => { await flows.flowBatalCheckIn(page, loginAs, readWorld()) })
  test('01 mengelola data tamu', async ({ page, loginAs }) => { await flows.flowDataTamu(page, loginAs, readWorld()) })
  test('01 bagan kamar dan ketersediaan', async ({ page, loginAs }) => { await flows.flowBaganKamar(page, loginAs, readWorld()) })
})
