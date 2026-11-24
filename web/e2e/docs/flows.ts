import { expect, type Locator, type Page } from 'playwright/test'
import { addDays } from '../support/factories'
import { TENANT, USER_PASSWORD } from '../support/env'
import { tx } from '../support/i18n'
import type { SeedState, UserKey } from '../support/state'
import { chooseRoomType, pickFirstReadyRoom, setDate } from '../support/ui'
import { shot, shotAround, shotPage } from './shot'
import { MANAGER, type DocsWorld } from './world'

/**
 * The flows of the user guide: one function per page of docs/user-guide, its steps marked `// STEP n:` with the numbers of the page's
 * "Langkah-langkah", and a screenshot at a key step (at most three per page). A flow drives the screens the way the guide tells the reader to;
 * the data it starts from is the hotel of world.ts. They are also the scripts of the future tutorial videos (docs/user-guide/_laporan.md).
 */
type LoginAs = (user: UserKey, path?: string) => Promise<void>

/** Chooses the value of a field that is a <select> or a group of radios with the same name. */
async function choose(scope: Locator, name: string, value: string): Promise<void> {
  const field = scope.locator(`[name="${name}"]`).first()
  const tag = await field.evaluate((e) => e.tagName.toLowerCase())
  if (tag === 'select') await field.selectOption(value)
  else await scope.locator(`[name="${name}"][value="${value}"]`).check()
}

// =====================================================================================================================
// 00-memulai

/** 00-memulai/login-dan-navigasi.md */
export async function flowLogin(page: Page, seed: SeedState): Promise<void> {
  // 00-memulai/mengganti-bahasa-dan-tampilan.md STEP 1: a browser that never chose a language shows the sign-in page in English
  await page.goto('/login')
  const card = page.locator('main form')
  await expect(card.locator('input[name=tenant_code]')).toBeVisible()
  await shot(card, 'memulai-bahasa-01', { pad: 24 })
  // ... and Bahasa Indonesia is chosen in the list at the bottom of the card (kept in this browser)
  await card.locator('select').last().selectOption('id')
  await expect(card.getByRole('button', { name: tx('login.signIn'), exact: true })).toBeVisible()
  // STEP 1: open the address of KamaraPMS: the sign-in page
  await page.goto('/login')
  await expect(card.locator('input[name=tenant_code]')).toBeVisible()
  // STEP 2-4: tenant code, e-mail and password (the picture shows the example of the guide; the real ones of the test follow)
  await card.locator('input[name=tenant_code]').fill('KAMARA')
  await card.locator('input[name=email]').fill('rina@hotelkamara.example')
  await card.locator('input[name=password]').fill('contoh-kata-sandi')
  await shot(card, 'memulai-login-04', { pad: 24 })
  await card.locator('input[name=tenant_code]').fill(TENANT)
  await card.locator('input[name=email]').fill(seed.users.frontdesk.email)
  await card.locator('input[name=password]').fill(USER_PASSWORD)
  // STEP 5: Masuk: the page Hari Ini, with the menu on the left and the top bar
  await card.getByRole('button', { name: tx('login.signIn'), exact: true }).click()
  await expect(page.getByTestId('topbar')).toBeVisible()
  await expect(page.getByTestId('strip')).toBeVisible()
  await shotPage(page, 'memulai-login-05')
  // STEP 6: the quick finder (Ctrl+K): a guest by name
  await page.getByTestId('open-search').click()
  const palette = page.getByTestId('command-palette')
  await palette.getByTestId('palette-input').fill('Santoso')
  await expect(palette.getByText('Budi Santoso').first()).toBeVisible()
  await shot(palette, 'memulai-login-06')
  await page.keyboard.press('Escape')
}

/** 00-memulai/mengganti-bahasa-dan-tampilan.md */
export async function flowBahasaTampilan(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1: before signing in, the language list at the bottom of the sign-in card: flowLogin makes that picture (memulai-bahasa-01)
  // STEP 3: after signing in: the account menu, Bahasa and Tema
  await loginAs('frontdesk', '/')
  await page.getByTestId('user-menu').click()
  const menu = page.getByTestId('user-menu-content')
  await expect(menu.getByTestId('language-id')).toBeVisible()
  await shot(menu, 'memulai-bahasa-03', { pad: 8 })
  await page.keyboard.press('Escape')
  // STEP 5: Akun saya: Tampilan
  await page.goto('/account')
  const themes = page.getByTestId('theme-switcher')
  await expect(themes).toBeVisible()
  await shot(page.locator('main'), 'memulai-bahasa-05', { pad: 0 })
}

/** 00-memulai/setup-awal-hotel.md: the step of the taxes (the other steps link to 13-pengaturan). */
export async function flowSetupPajak(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 5a: Pengaturan → Pajak & biaya layanan → Pajak baru (the hotel of the pictures has its taxes already: the form is filled and left unsaved)
  await loginAs('admin', '/setup/taxes')
  const tax = page.locator('main').getByTestId('section-tax')
  await tax.getByTestId('new-item').click()
  await tax.locator('input[name=code]').fill('PBJT')
  await tax.locator('input[name=name]').fill('Pajak hotel (PBJT)')
  await tax.locator('input[name=rate]').fill('10')
  await tax.getByLabel(tx('rateItems.taxAccount')).fill('2410')
  await tax.locator('[name=tax_kind]').selectOption('LOCAL')
  await tax.locator('input[name=tax_on_service]').check()
  await shot(tax, 'memulai-setup-pajak-01')
  await tax.getByRole('button', { name: tx('common.cancel'), exact: true }).click()
  // STEP 5c: Kode biaya → Ubah the room code → Aturan: biaya servis then pajak, Simpan aturan
  await page.goto('/setup/charge-codes')
  // the row of the code ROOM (not ROOM_EXEMPT): its first cell starts with ROOM and the mark "sistem"
  const row = page.getByRole('row').filter({ hasText: /^\s*ROOM(?!_)/ }).first()
  await row.getByRole('button', { name: tx('common.edit'), exact: true }).click()
  const form = page.getByTestId('code-form')
  await expect(form.getByTestId('save-rules')).toBeVisible()
  await shot(form, 'memulai-setup-pajak-02')
  // STEP 5d: the list shows the rules of each code in their order
  await page.goto('/setup/charge-codes')
  const minibar = page.getByRole('row').filter({ hasText: /^\s*MINIBAR/ }).first()
  await expect(minibar).toBeVisible()
  await page.evaluate(() => window.scrollTo(0, 0))
  await shotAround([page.locator('main table thead').first(), minibar], 'memulai-setup-pajak-03', { pad: 8 })
}

/** 00-memulai/memahami-tanggal-bisnis.md */
export async function flowTanggalBisnis(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1: the business date in the top bar (with the state of the night audit for whoever may run it)
  await loginAs('admin', '/')
  const chip = page.getByTestId('business-date')
  await expect(chip).toBeVisible()
  await shot(chip, 'memulai-tanggal-bisnis-01', { pad: 16 })
  // STEP 2: the date beside the title of Tamu hari ini
  await page.goto('/arrivals')
  await expect(page.getByTestId('front-desk-date')).toBeVisible()
  await shot(page.locator('main header, main [data-slot=page-header]').first(), 'memulai-tanggal-bisnis-02')
}

/** 00-memulai/peran-dan-hak-akses.md */
export async function flowPeran(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 3: Pengaturan → Peran → Peran baru: a name and the permissions of a group (not saved: the pictures need no new role)
  await loginAs('admin', '/setup/roles/new')
  await page.locator('input[name=name]').fill('Resepsionis')
  await page.locator('input[name=description]').fill('Reservasi, check-in, check-out, dan pembayaran tamu')
  const group = page.locator('[data-testid^=group-]').filter({ has: page.locator('[data-permission="frontdesk.checkin"]') }).first()
  await group.locator('summary, button').first().click().catch(() => { /* the box may already be open */ })
  for (const code of ['frontdesk.checkin', 'frontdesk.checkout', 'frontdesk.room_move']) await page.locator(`[data-permission="${code}"]`).check()
  await shot(group, 'memulai-peran-03')
  await page.getByTestId('discard').click()
  // the page of someone whose role lacks the permission: a sentence instead of the page
  await loginAs('housekeeping', '/reservations')
  await expect(page.getByTestId('no-access')).toBeVisible()
  await shotAround([page.locator('main h1').first(), page.getByTestId('no-access')], 'memulai-peran-akses', { pad: 24 })
}

// =====================================================================================================================
// 01-front-desk

/** 01-front-desk/membuat-reservasi.md */
export async function flowReservasiBaru(page: Page, loginAs: LoginAs, w: DocsWorld, seed: SeedState): Promise<void> {
  const arrival = addDays(w.businessDate, 10)
  // STEP 1: Front Desk → Reservasi → Reservasi baru
  await loginAs('frontdesk', '/reservations')
  await page.getByTestId('new').click()
  // STEP 2: Datang, Pulang, Dewasa
  const search = page.getByTestId('search-form')
  await setDate(search, 'arrival', arrival)
  await setDate(search, 'departure', addDays(arrival, 2))
  await search.locator('input[name=adults]').fill('2')
  // STEP 3: Cari: the room types and rate plans with their estimate
  await search.getByRole('button', { name: tx('newReservation.search') }).click()
  const results = page.getByTestId('results')
  await expect(results).toBeVisible()
  await shot(results, 'frontdesk-reservasi-baru-03')
  // STEP 4: Pesan on the row of the room type
  const std = seed.roomTypes.find((t) => t.code === 'STD') ?? seed.roomTypes[0]!
  await page.getByTestId(`pick-${std.code}-RO`).click()
  // STEP 5: the booker: search and pick
  const book = page.getByTestId('book-form')
  await book.locator('input[name=guest_q]').fill('Hartono')
  await book.getByTestId('find-guest').click()
  await book.getByTestId(`guest-${w.rudi.code}`).click()
  await expect(book.getByTestId('chosen-guest')).toContainText('Hartono')
  // STEP 6: Sumber and Catatan
  await book.locator('select[name=source]').selectOption('PHONE')
  await book.locator('input[name=remarks], textarea[name=remarks]').first().fill('Tiba sekitar pukul 20.00, minta kamar tenang')
  await expect(book.locator('input[name=confirm]')).toBeChecked()
  await shot(book, 'frontdesk-reservasi-baru-06')
  // STEP 7: Pesan dan konfirmasi
  await book.getByRole('button', { name: tx('newReservation.bookAndConfirm') }).click()
  // STEP 8: the new reservation
  await expect(page).toHaveURL(/\/reservations\/\d+$/)
  await expect(page.getByTestId('header-card')).toBeVisible()
  await shot(page.getByTestId('header-card'), 'frontdesk-reservasi-baru-08')
}

/** 01-front-desk/mengonfirmasi-dan-mengubah-reservasi.md */
export async function flowKonfirmasiReservasi(page: Page, loginAs: LoginAs, w: DocsWorld): Promise<void> {
  // STEP 1: Reservasi, the chip Draf
  await loginAs('frontdesk', '/reservations')
  await page.getByTestId('preset-draft').click()
  // STEP 2: open the draft
  const row = page.getByTestId(`res-${w.future.hendra.confirmation}`)
  await expect(row).toBeVisible()
  await row.getByRole('link').first().click()
  await expect(page.getByTestId('draft-note')).toBeVisible()
  await shot(page.locator('main'), 'frontdesk-konfirmasi-02', { pad: 0 })
  // STEP 3: Konfirmasi
  await page.getByTestId('confirm').click()
  await expect(page.getByTestId('confirm')).toHaveCount(0)
  // STEP 5: Ubah reservasi: a special request
  await page.getByTestId('edit-reservation').click()
  const dialog = page.getByTestId('reservation-edit-dialog')
  await expect(dialog).toBeVisible()
  await dialog.locator('[name=special_request]').fill('Kamar di lantai atas, jauh dari lift')
  await shot(dialog, 'frontdesk-konfirmasi-05')
  // STEP 6: Simpan on the part that changed, then Tutup
  await dialog.getByTestId('save-header-fields').click()
  await expect(dialog.getByTestId('save-header-fields')).toBeDisabled()
  await dialog.getByTestId('edit-close').click()
  await expect(page.getByTestId('special-request')).toContainText('lantai atas')
}

/** 01-front-desk/menetapkan-kamar.md */
export async function flowTetapkanKamar(page: Page, loginAs: LoginAs, w: DocsWorld): Promise<void> {
  // STEP 1-2: the reservation without a room, Tetapkan kamar on its room
  await loginAs('frontdesk', `/reservations/${w.future.fitri.id}`)
  const line = w.future.fitri.lineId
  await page.getByTestId(`assign-${line}`).click()
  const form = page.getByTestId(`assign-form-${line}`)
  await expect(form).toBeVisible()
  // STEP 3: Kamar kosong: the free rooms of the type
  await form.getByTestId('pick-room').click()
  const picker = page.getByTestId('room-picker')
  await expect(picker.locator('[data-testid^=room-option-]').first()).toBeVisible()
  await shot(picker, 'frontdesk-tetapkan-kamar-03')
  // STEP 4: click a room (a clean one, found with Cari nomor kamar): it is assigned at once
  await picker.getByTestId('room-search').fill('208')
  await picker.locator('[data-testid^=room-option-]').first().click()
  await expect(page.getByTestId(`room-number-${line}`)).toBeVisible()
  // STEP 6-7: arrivals of today without a room: Tetapkan otomatis
  await page.goto('/arrivals')
  await page.getByTestId('auto-assign-open').click()
  const dialog = page.getByRole('dialog').filter({ hasText: tx('autoAssign.title') })
  await expect(dialog.getByTestId('auto-confirm')).toBeEnabled()
  await shot(dialog, 'frontdesk-tetapkan-kamar-07')
  // STEP 8: Tetapkan {n} kamar, then Tutup
  await dialog.getByTestId('auto-confirm').click()
  await expect(dialog.getByTestId('auto-close')).toBeVisible()
  await dialog.getByTestId('auto-close').click()
}

/** 01-front-desk/menerima-deposit.md */
export async function flowDeposit(page: Page, loginAs: LoginAs, w: DocsWorld): Promise<void> {
  // STEP 1: the reservation, card Terima deposit
  await loginAs('frontdesk', `/reservations/${w.future.fitri.id}`)
  const form = page.getByTestId('deposit-form')
  await expect(form).toBeVisible()
  // (Perlu diperhatikan) cash needs an open cashier shift: the notice
  await form.locator('[name=deposit_amount]').fill('1000000')
  await choose(form, 'deposit_method', 'CASH')
  const card = page.getByTestId('deposit-card')
  await expect(card.getByTestId('shift-required')).toBeVisible()
  await shot(card, 'frontdesk-deposit-tunai')
  // STEP 2-4: Jumlah, Metode Kartu, Referensi
  await choose(form, 'deposit_method', 'CARD')
  await form.locator('[name=deposit_reference]').fill('EDC 004512')
  await shot(card, 'frontdesk-deposit-04')
  // STEP 5: Terima deposit
  await form.getByRole('button', { name: tx('reservation.takeDepositButton') }).click()
  await expect(page.getByTestId('notice')).toBeVisible()
  await shot(page.getByTestId('notice'), 'frontdesk-deposit-05', { pad: 8 })
}

/** 01-front-desk/membatalkan-reservasi-dan-no-show.md */
export async function flowBatalReservasi(page: Page, loginAs: LoginAs, w: DocsWorld): Promise<void> {
  // STEP 1-2: the reservation, Batalkan reservasi
  await loginAs('frontdesk', `/reservations/${w.future.yusuf.id}`)
  await page.getByTestId('cancel').click()
  // STEP 3: the reason
  const form = page.locator('form[data-testid=reason-form]')
  await form.locator('input[name=reason]').fill('Tamu membatalkan perjalanan karena urusan keluarga')
  await shot(form, 'frontdesk-batal-03')
  // STEP 4: Batal (the red button does the cancelling)
  await form.getByRole('button', { name: tx('common.cancel'), exact: true }).click()
  await expect(page.getByTestId('reinstate')).toBeVisible()
  // STEP 6: Posting biaya pembatalan
  await page.getByTestId('post-cancel-fee').click()
  const fee = page.getByTestId('fee-form')
  await fee.locator('[name=fee_amount]').fill('500000')
  await fee.locator('[name=fee_reason]').fill('Dibatalkan kurang dari 48 jam sebelum kedatangan')
  await shot(fee, 'frontdesk-batal-06')
  await fee.getByRole('button', { name: tx('reservation.postFee') }).click()
  await expect(page.getByTestId('notice')).toBeVisible()
}

/** 01-front-desk/check-in.md */
export async function flowCheckIn(page: Page, loginAs: LoginAs, w: DocsWorld): Promise<void> {
  const budi = w.arrivals.budi
  // STEP 1: Front Desk → Tamu hari ini → Kedatangan
  await loginAs('frontdesk', '/arrivals')
  const panel = page.getByTestId('panel-arrivals')
  await expect(panel.getByTestId(`arrival-${budi.lineId}`)).toBeVisible()
  await shot(panel, 'frontdesk-checkin-01')
  // STEP 2: Check-in on the guest's row
  await page.getByTestId(`open-${budi.lineId}`).click()
  const sheet = page.getByTestId('checkin-sheet')
  // STEP 3: Tamu: check the data, the identity
  await expect(sheet.getByTestId('step-guest')).toBeVisible()
  await sheet.locator('input[name=id_type]').fill('KTP')
  await sheet.locator('input[name=id_number]').fill('3174012507850003')
  await sheet.getByTestId('checkin-next').click()
  // STEP 4: Kamar: the assigned room
  await expect(sheet.getByTestId('step-room')).toBeVisible()
  await sheet.getByTestId('checkin-next').click()
  // STEP 5: Deposit: method and reference
  await expect(sheet.getByTestId('step-deposit')).toBeVisible()
  await sheet.getByTestId('deposit-method-CARD').click()
  await sheet.locator('input[name=deposit_reference]').fill('EDC 004513')
  await shot(sheet.locator('form').first(), 'frontdesk-checkin-05')
  // STEP 6: Terima deposit dan check-in
  await sheet.getByTestId('checkin-submit').click()
  // STEP 7: Selesai: the registration card, the receipt
  await expect(sheet.getByTestId('step-done')).toBeVisible()
  await shot(sheet.locator('form').first(), 'frontdesk-checkin-07')
  await sheet.getByTestId('checkin-close').click()
}

/** 01-front-desk/walk-in.md */
export async function flowWalkIn(page: Page, loginAs: LoginAs, seed: SeedState): Promise<void> {
  const std = seed.roomTypes.find((t) => t.code === 'STD') ?? seed.roomTypes[0]!
  // STEP 1: Front Desk → Walk-in
  await loginAs('frontdesk', '/walk-in')
  const form = page.getByTestId('walkin-form')
  const bd = await page.getByTestId('walkin-form').locator('input[name=departure]').inputValue()
  // STEP 2: Keberangkatan
  await setDate(form, 'departure', addDays(bd, 1))
  // STEP 3: Tipe kamar and Kamar
  await chooseRoomType(form, 'room_type', std.id)
  await pickFirstReadyRoom(form, 'room', tx('walkIn.notReadyTag'))
  // STEP 5: the new guest
  await form.locator('input[name=first_name]').fill('Wayan')
  await form.locator('input[name=last_name]').fill('Sudarsana')
  await shot(form, 'frontdesk-walkin-05')
  // STEP 6: Check-in: the stay page
  await form.getByTestId('walkin-submit').click()
  await expect(page).toHaveURL(/\/stays\/\d+$/)
  await expect(page.getByTestId('guest-name')).toContainText('Sudarsana')
  await shot(page.getByTestId('summary'), 'frontdesk-walkin-06')
}

/** 01-front-desk/pindah-kamar.md */
export async function flowPindahKamar(page: Page, loginAs: LoginAs, w: DocsWorld, seed: SeedState): Promise<void> {
  const siti = w.inHouse.siti
  // STEP 1-2: Tamu hari ini → Menginap → ⋮ → Pindah kamar
  await loginAs('frontdesk', '/in-house')
  await page.getByTestId(`more-${siti.stayNumber}`).click()
  await page.getByTestId(`moveRoom-${siti.stayNumber}`).click()
  const form = page.getByTestId('move-form')
  await expect(form).toBeVisible()
  // STEP 3: Tipe kamar, Kamar, Alasan
  const sup = seed.roomTypes.find((t) => t.code === 'SUP') ?? seed.roomTypes[0]!
  await chooseRoomType(form, 'room_type', sup.id)
  await pickFirstReadyRoom(form, 'room', tx('stayActions.notReadyTag'))
  await form.locator('input[name=reason]').fill('AC kamar tidak dingin')
  await shot(form, 'frontdesk-pindah-03')
  // STEP 4: Pindahkan
  await form.getByRole('button', { name: tx('stayActions.move'), exact: true }).click()
  await expect(page.getByTestId('notice')).toBeVisible()
  await shot(page.locator('main'), 'frontdesk-pindah-04', { pad: 0 })
}

/** 01-front-desk/perpanjang-atau-persingkat.md */
export async function flowPerpanjang(page: Page, loginAs: LoginAs, w: DocsWorld): Promise<void> {
  const agus = w.inHouse.agus
  // STEP 1: the stay page (Menginap → Lihat)
  await loginAs('frontdesk', `/stays/${agus.stayId}`)
  // STEP 2: Perpanjang / persingkat
  await page.getByTestId('open-departure').click()
  const form = page.getByTestId('departure-form')
  const current = await form.locator('input[name=departure]').inputValue()
  // STEP 3: the new departure
  await setDate(form, 'departure', addDays(current, 1))
  await shot(form, 'frontdesk-perpanjang-03')
  // STEP 4: Simpan
  await form.getByRole('button', { name: tx('common.save'), exact: true }).click()
  await expect(page.getByTestId('notice')).toBeVisible()
  await shot(page.getByTestId('notice'), 'frontdesk-perpanjang-04', { pad: 8 })
}

/** 01-front-desk/mengubah-tarif-tamu-menginap.md, and the approval of 00-memulai/meminta-persetujuan.md */
export async function flowUbahTarif(page: Page, loginAs: LoginAs, w: DocsWorld): Promise<void> {
  const made = w.inHouse.made
  // STEP 1-2: Tamu hari ini → Menginap → ⋮ → Ubah tarif
  await loginAs('frontdesk', '/in-house')
  await page.getByTestId(`more-${made.stayNumber}`).click()
  await page.getByTestId(`editRate-${made.stayNumber}`).click()
  const dialog = page.getByTestId('edit-rate-dialog')
  await expect(dialog.getByTestId('rate-nights')).toBeVisible()
  // STEP 3: the first night not yet charged, Sisa malam, the new rate and the reason
  const open = dialog.locator('[data-testid^=night-]').filter({ has: page.locator('input[name=night]:not([disabled])') })
  await open.first().locator('input[name=night]').check()
  await choose(dialog, 'apply_to', 'REMAINING')
  await dialog.locator('input[name=amount]').fill('700000')
  await dialog.locator('input[name=reason], textarea[name=reason]').first().fill('Tamu langganan, harga khusus dari manajer')
  await shot(dialog, 'frontdesk-ubah-tarif-03')
  // STEP 4: Terapkan: a lower rate asks the approver
  await dialog.getByTestId('rate-apply').click()
  const approval = page.getByTestId('approval-dialog')
  await expect(approval).toBeVisible()
  // 00-memulai/meminta-persetujuan.md STEP 2-3: the approver types e-mail and password
  await approval.locator('input[name=approval_email]').fill(MANAGER.email)
  await approval.locator('input[name=approval_password]').fill(USER_PASSWORD)
  await shot(approval, 'memulai-persetujuan-02')
  // STEP 5 (meminta-persetujuan STEP 4): Setujui
  await approval.getByTestId('approval-submit').click()
  await expect(page.getByTestId('toast-success').filter({ hasText: tx('frontDesk.rateEdit.saved') }).first()).toBeVisible()
}

/** 01-front-desk/check-out.md */
export async function flowCheckOut(page: Page, loginAs: LoginAs, w: DocsWorld): Promise<void> {
  const putri = w.inHouse.putri
  // STEP 1: Tamu hari ini → Keberangkatan
  await loginAs('frontdesk', '/departures')
  const panel = page.getByTestId('panel-departures')
  await expect(panel.getByTestId(`stay-${putri.stayNumber}`)).toBeVisible()
  await shot(panel, 'frontdesk-checkout-01')
  // STEP 2: Bayar & check-out
  await panel.getByTestId(`checkOut-${putri.stayNumber}`).click()
  const wizard = page.getByTestId('checkout-wizard')
  await expect(wizard.getByTestId('step-departure')).toBeVisible()
  const early = wizard.locator('input[name=confirm_early]')
  if (await early.count()) await early.check() // only when the day of setup is still open (world.audited false)
  // STEP 3: pay the balance: Jumlah is filled, Metode, Referensi
  const pay = wizard.getByTestId('checkout-pay').first()
  await pay.locator('select[name=pay_method]').selectOption('CARD')
  await pay.locator('input[name=pay_reference]').fill('EDC 004514')
  await shot(wizard, 'frontdesk-checkout-03')
  // STEP 4: Bayar
  await pay.getByTestId('checkout-pay-submit').click()
  await expect(wizard.getByTestId('checkout-paid')).toBeVisible()
  // STEP 5: Check-out
  await wizard.getByTestId('checkout-submit').click()
  await expect(page.getByTestId('notice')).toBeVisible()
  await shotAround([page.getByTestId('notice'), page.getByTestId('summary')], 'frontdesk-checkout-05')
}

/** 01-front-desk/membatalkan-check-in.md */
export async function flowBatalCheckIn(page: Page, loginAs: LoginAs, w: DocsWorld): Promise<void> {
  // STEP 1: the stay page (a supervisor: the permission to reverse a check-in)
  await loginAs('supervisor', `/stays/${w.inHouse.andreas.stayId}`)
  // STEP 2: Batalkan check-in, the reason
  await page.getByTestId('reverse').click()
  const form = page.getByTestId('reverse-form')
  await form.locator('input[name=reason]').fill('Salah memilih tamu saat check-in')
  await shot(form, 'frontdesk-batal-checkin-02')
  // STEP 3: Batalkan
  await form.getByRole('button', { name: tx('stay.reverse') }).click()
  await expect(page.getByTestId('notice')).toBeVisible()
  await shot(page.getByTestId('notice'), 'frontdesk-batal-checkin-03', { pad: 8 })
}

/** 01-front-desk/mengelola-data-tamu.md */
export async function flowDataTamu(page: Page, loginAs: LoginAs, w: DocsWorld): Promise<void> {
  // STEP 1-2: Front Desk → Tamu → Tamu baru
  await loginAs('frontdesk', '/guests')
  await page.getByTestId('new-guest').click()
  const form = page.getByTestId('create-form')
  // STEP 3: the fields
  await form.locator('input[name=first_name]').fill('Ketut')
  await form.locator('input[name=last_name]').fill('Ardana')
  await form.locator('input[name=email]').fill('ketut.ardana@contoh.example')
  await form.locator('input[name=phone]').fill('+62 813 3900 1214')
  await form.locator('input[name=nationality]').fill('ID')
  await form.locator('input[name=id_type]').fill('KTP')
  await form.locator('input[name=id_number]').fill('5103041208900002')
  await form.locator('input[name=city]').fill('Denpasar')
  await form.locator('select[name=gender]').selectOption('MALE')
  await shot(form, 'frontdesk-tamu-03')
  // STEP 4: Simpan
  await form.getByRole('button', { name: tx('common.save'), exact: true }).click()
  await expect(page.getByTestId('created')).toBeVisible()
  // STEP 6: a guest's profile and history
  await page.goto(`/guests/${w.arrivals.budi.guest.id}`)
  await expect(page.getByTestId('no-history').or(page.locator('main table')).first()).toBeVisible()
  await shot(page.locator('main'), 'frontdesk-tamu-06', { pad: 0 })
}

/** 01-front-desk/bagan-kamar-dan-ketersediaan.md */
export async function flowBaganKamar(page: Page, loginAs: LoginAs, w: DocsWorld): Promise<void> {
  // STEP 1: Front Desk → Bagan kamar
  await loginAs('frontdesk', '/reservations/tape')
  await expect(page.getByTestId('tape')).toBeVisible()
  await shotPage(page, 'frontdesk-bagan-01')
  // STEP 3: click a booking: its menu
  const room = w.arrivals.dewi.room!
  await page.getByTestId(`room-${room}`).evaluate((e) => e.scrollIntoView({ block: 'center' }))
  await page.locator(`[data-testid^="bar-${room}-"]`).first().click()
  const menu = page.getByTestId('tape-menu')
  await expect(menu).toBeVisible()
  await shotAround([page.getByTestId(`room-${room}`), menu], 'frontdesk-bagan-03', { pad: 24 })
  await page.keyboard.press('Escape')
  // STEP 6: Front Desk → Ketersediaan
  await page.goto('/availability')
  await expect(page.getByTestId('calendar')).toBeVisible()
  await shotAround([page.locator('main h1').first(), page.getByTestId('calendar')], 'frontdesk-ketersediaan-06')
}
