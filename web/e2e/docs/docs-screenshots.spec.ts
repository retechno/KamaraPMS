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

  // --- 02-housekeeping
  test('02 status kamar', async ({ page, loginAs }) => { await flows.flowStatusKamar(page, loginAs, readWorld()) })
  test('02 mengubah status kebersihan', async ({ page, loginAs }) => { await flows.flowStatusKebersihan(page, loginAs) })
  test('02 daftar pembersihan dan tugas saya', async ({ page, loginAs }) => { await flows.flowDaftarPembersihan(page, loginAs) })
  test('02 blokir kamar', async ({ page, loginAs }) => { await flows.flowBlokirKamar(page, loginAs, readWorld()) })
  test('02 laporan perawatan', async ({ page, loginAs }) => { await flows.flowPerawatan(page, loginAs, readWorld()) })
  test('02 barang tertinggal', async ({ page, loginAs }) => { await flows.flowBarangTertinggal(page, loginAs, readWorld()) })

  // --- 03-kasir-dan-pembayaran
  test('03 posting biaya ke folio', async ({ page, loginAs }) => { await flows.flowPostingBiaya(page, loginAs, readWorld()) })
  test('03 menerima pembayaran', async ({ page, loginAs }) => { await flows.flowPembayaran(page, loginAs, readWorld()) })
  test('03 reverse dan penyesuaian', async ({ page, loginAs, seed }) => { await flows.flowKoreksi(page, loginAs, readWorld(), seed) })
  test('03 void dan refund', async ({ page, loginAs, seed }) => { await flows.flowVoidRefund(page, loginAs, readWorld(), seed) })
  test('03 transfer antar folio', async ({ page, loginAs, seed }) => { await flows.flowTransferFolio(page, loginAs, readWorld(), seed) })
  test('03 grup tagihan dan cetak', async ({ page, loginAs }) => { await flows.flowGrupTagihan(page, loginAs, readWorld()) })
  test('03 shift kasir', async ({ page, loginAs, seed }) => { await flows.flowShiftKasir(page, loginAs, readWorld(), seed) })
  test('03 posting biaya kamar', async ({ page, loginAs }) => { await flows.flowBiayaKamar(page, loginAs) })
  test('03 membuka folio untuk outlet', async ({ page, loginAs }) => { await flows.flowFolioOutlet(page, loginAs, readWorld()) })

  // --- 04-pos
  test('04 menyiapkan outlet', async ({ page, loginAs }) => { await flows.flowMenyiapkanOutlet(page, loginAs) })
  test('04 mengambil pesanan', async ({ page, loginAs }) => { await flows.flowAmbilPesanan(page, loginAs) })
  test('04 layar dapur', async ({ page, loginAs }) => { await flows.flowLayarDapur(page, loginAs) })
  test('04 membayar bill', async ({ page, loginAs }) => { await flows.flowBayarBill(page, loginAs) })
  test('04 bebankan ke kamar', async ({ page, loginAs }) => { await flows.flowBebankanKamar(page, loginAs, readWorld()) })
  test('04 void diskon komplimen', async ({ page, loginAs, seed }) => { await flows.flowVoidPos(page, loginAs, seed) })
  test('04 laporan outlet', async ({ page, loginAs }) => { await flows.flowLaporanOutlet(page, loginAs) })

  // --- 05-banquet (the tests after "05 membuat acara dan sesi" use its event)
  test('05 menyiapkan banquet', async ({ page, loginAs }) => { await flows.flowMenyiapkanBanquet(page, loginAs) })
  test('05 membuat acara dan sesi', async ({ page, loginAs, data }) => { await flows.flowAcaraBaru(page, loginAs, readWorld(), data) })
  test('05 tentatif dan konfirmasi', async ({ page, loginAs, data }) => { await flows.flowTentatifKonfirmasi(page, loginAs, readWorld(), data) })
  test('05 item paket dan deposit', async ({ page, loginAs, data }) => { await flows.flowItemDeposit(page, loginAs, data) })
  test('05 posting sesi', async ({ page, loginAs, data }) => { await flows.flowPostingSesi(page, loginAs, data) })
  test('05 beo dan dokumen', async ({ page, loginAs, data }) => { await flows.flowBeoDokumen(page, loginAs, data) })
  test('05 membatalkan acara', async ({ page, loginAs, data }) => { await flows.flowBatalAcara(page, loginAs, readWorld(), data) })

  // --- 06-corporate-group-city-ledger (the tests after "06 perusahaan" use its company, in this order)
  test('06 perusahaan', async ({ page, loginAs }) => { await flows.flowPerusahaan(page, loginAs) })
  test('06 grup dan master folio', async ({ page, loginAs }) => { await flows.flowGrup(page, loginAs, readWorld()) })
  test('06 transfer ke piutang perusahaan', async ({ page, loginAs, data }) => { await flows.flowTransferPiutang(page, loginAs, data) })
  test('06 invoice dan penerimaan', async ({ page, loginAs, data }) => { await flows.flowInvoice(page, loginAs, data) })
  test('06 nota kredit', async ({ page, loginAs, data, seed }) => { await flows.flowNotaKredit(page, loginAs, data, seed) })
  test('06 surat peringatan', async ({ page, loginAs }) => { await flows.flowSuratPeringatan(page, loginAs) })

  // --- 07-accounting
  test('07 bagan akun', async ({ page, loginAs }) => { await flows.flowBaganAkun(page, loginAs) })
  test('07 jurnal manual', async ({ page, loginAs, seed }) => { await flows.flowJurnalManual(page, loginAs, readWorld(), seed) })
  test('07 periode dan tahun buku', async ({ page, loginAs }) => { await flows.flowPeriode(page, loginAs) })
  test('07 departemen', async ({ page, loginAs }) => { await flows.flowDepartemen(page, loginAs) })
  test('07 laporan keuangan', async ({ page, loginAs }) => { await flows.flowLaporanKeuangan(page, loginAs) })
  // --- 08-keuangan-back-office
  test('08 utang usaha', async ({ page, loginAs, data }) => { await flows.flowUtangUsaha(page, loginAs, readWorld(), data) })
  test('08 pajak dan faktur', async ({ page, loginAs }) => { await flows.flowPajak(page, loginAs) })
  test('08 rekonsiliasi bank', async ({ page, loginAs }) => { await flows.flowRekonsiliasiBank(page, loginAs, readWorld()) })
  test('08 settlement kartu', async ({ page, loginAs }) => { await flows.flowSettlementKartu(page, loginAs, readWorld()) })
  test('08 anggaran', async ({ page, loginAs, seed }) => { await flows.flowAnggaran(page, loginAs, seed) })

})
