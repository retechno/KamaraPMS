import { expect, type Locator, type Page } from 'playwright/test'
import { addDays, type Factories } from '../support/factories'
import { TENANT, USER_PASSWORD } from '../support/env'
import { tx } from '../support/i18n'
import { ROLES, type SeedState, type UserKey } from '../support/state'
import { chooseRoomType, pickCombo, pickFirstReadyRoom, setDate } from '../support/ui'
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

// =====================================================================================================================
// 02-housekeeping

/** The suites of the second and third floor: world.ts never puts a guest in a suite, so these rooms are free to block. */
const SUITE_FLOOR_2 = '210'
const SUITE_FLOOR_3 = '310'

/** 02-housekeeping/status-kamar.md */
export async function flowStatusKamar(page: Page, loginAs: LoginAs, w: DocsWorld): Promise<void> {
  // STEP 1: Kamar → Status kamar: the board by floor, with the count line and the filters
  await loginAs('frontdesk', '/room-status')
  await expect(page.getByTestId('counts')).toBeVisible()
  await expect(page.getByTestId('tile-110')).toBeVisible()
  await shotAround([page.locator('main h1'), page.getByTestId('filters'), page.getByTestId('tile-110')], 'housekeeping-status-kamar-01')
  // STEP 3: a filter (the reserved rooms), then all again
  await page.getByTestId('filter-occ-RESERVED').click()
  await page.getByTestId('filter-occ-ALL').click()
  // STEP 4: a room opens the room panel (an occupied room: Made Wirawan)
  await page.getByTestId(`tile-${w.inHouse.made.room}`).click()
  const sheet = page.getByTestId('room-sheet')
  await expect(sheet.getByTestId('rp-number')).toContainText(w.inHouse.made.room)
  await shot(sheet, 'housekeeping-status-kamar-04', { pad: 0 })
  await sheet.getByTestId('rp-close').click()
  // STEP 5: Muat ulang
  await page.getByTestId('refresh').click()
}

/** 02-housekeeping/mengubah-status-kebersihan.md */
export async function flowStatusKebersihan(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1-2: Kamar → Housekeeping, the chip Kotor
  await loginAs('housekeeping', '/housekeeping')
  await page.getByTestId('filter-DIRTY').click()
  const rows = page.locator('main [data-testid^="room-"]')
  await expect(rows.first()).toBeVisible()
  // STEP 3: the next step of a dirty room is Mulai bersihkan
  const row = rows.filter({ has: page.getByTestId('act-CLEANING') }).first()
  await expect(row.getByTestId('act-CLEANING')).toBeVisible()
  await shotAround([page.locator('main h1'), rows.nth(Math.min(5, (await rows.count()) - 1))], 'housekeeping-status-kebersihan-03')
  // STEP 4: the room becomes Dibersihkan and leaves the filtered list
  const number = ((await row.getAttribute('data-testid')) ?? '').replace('room-', '')
  await row.getByTestId('act-CLEANING').click()
  await expect(page.getByTestId(`room-${number}`)).toHaveCount(0)

  // STEP 6: Status kamar: the supervisor ticks a whole floor
  await loginAs('admin', '/room-status')
  await page.getByTestId('pick-floor-1').click()
  const bar = page.getByTestId('bulk-bar')
  // STEP 7: the bulk bar with the count per action (the picture only: the floor is left as it is)
  await expect(bar.getByTestId('bulk-count')).toBeVisible()
  await shotPage(page, 'housekeeping-status-kebersihan-07')
  await bar.getByTestId('bulk-cancel').click()
  await expect(bar).toHaveCount(0)
}

/** 02-housekeeping/daftar-pembersihan-dan-tugas-saya.md */
export async function flowDaftarPembersihan(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1-2: Kamar → Daftar pembersihan → Buat daftar hari ini
  await loginAs('admin', '/housekeeping/tasks')
  await page.getByTestId('generate').click()
  await expect(page.getByTestId('notice')).toBeVisible()
  await expect(page.getByTestId('tasks')).toBeVisible()
  await shot(page.locator('main'), 'housekeeping-daftar-02', { pad: 0 })
  // STEP 3: the open tasks of the second floor ticked
  const floors = page.getByTestId('floors')
  await floors.getByTestId('floor-2').click()
  // STEP 4: given to the housekeeper
  const form = page.getByTestId('assign-form')
  await pickCombo(form, 'assign_to', ROLES.housekeeping.fullName)
  await shotAround([floors, form], 'housekeeping-daftar-04')
  await form.getByTestId('assign').click()
  await expect(page.getByTestId('notice')).toBeVisible()

  // STEP 6-7: the housekeeper's own cards on Tugas saya: Mulai bersihkan
  await loginAs('housekeeping', '/housekeeping/my')
  const card = page.locator('[data-testid^="card-"]').first()
  await expect(card).toBeVisible()
  const number = ((await card.getAttribute('data-testid')) ?? '').replace('card-', '')
  await shotAround([page.locator('main h1'), card], 'housekeeping-daftar-07')
  await page.getByTestId(`start-${number}`).click()
  // STEP 8: Selesai: siap diperiksa
  await page.getByTestId(`finish-${number}`).click()
  await expect(page.getByTestId(`finish-${number}`)).toHaveCount(0)
}

/** 02-housekeeping/blokir-kamar-ooo-oos.md */
export async function flowBlokirKamar(page: Page, loginAs: LoginAs, w: DocsWorld): Promise<void> {
  const from = addDays(w.businessDate, 1)
  // STEP 1: Kamar → Blokir kamar
  await loginAs('admin', '/room-blocks')
  // STEP 2: the form Blokir baru
  const form = page.getByTestId('block-form')
  await pickCombo(form, 'room_id', SUITE_FLOOR_3)
  await form.locator('select[name=block_type]').selectOption('OOS')
  await setDate(form, 'start_date', from)
  await setDate(form, 'end_date', addDays(from, 3))
  await form.locator('input[name=reason]').fill('Pengecatan ulang dinding kamar')
  await shot(form, 'housekeeping-blokir-02')
  // STEP 3: Buat blokir: the bar on the calendar and the row of the table
  await form.getByRole('button', { name: tx('roomBlocks.create'), exact: true }).click()
  await expect(page.getByTestId(`cal-${SUITE_FLOOR_3}`).getByTestId('bar')).toBeVisible()
  const table = page.locator('main [data-slot=card]').filter({ hasText: tx('roomBlocks.activeInView') }).last()
  await expect(table.getByText('Pengecatan ulang dinding kamar')).toBeVisible()
  await shot(table, 'housekeeping-blokir-03')
}

/** 02-housekeeping/laporan-perawatan.md */
export async function flowPerawatan(page: Page, loginAs: LoginAs, w: DocsWorld): Promise<void> {
  const what = 'Keran wastafel bocor, air menetes terus'
  // STEP 1: Kamar → Perawatan → Laporkan masalah (the housekeeper)
  await loginAs('housekeeping', '/maintenance')
  const newBtn = page.getByTestId('new-request')
  const form = page.getByTestId('request-form')
  await expect(newBtn.or(form).first()).toBeVisible()
  if (!(await form.isVisible())) await newBtn.click()
  // STEP 2: the form
  await pickCombo(form, 'room_id', `${SUITE_FLOOR_2} ·`)
  await form.locator('select[name=category]').selectOption('PLUMBING')
  await form.locator('select[name=priority]').selectOption('HIGH')
  await form.locator('input[name=description]').fill(what)
  await shot(form, 'housekeeping-perawatan-02')
  // STEP 3: Laporkan
  await form.getByRole('button', { name: tx('maintenance.reportButton'), exact: true }).click()
  await expect(page.getByTestId('notice')).toBeVisible()

  // STEP 4: the engineer opens the request
  await loginAs('admin', '/maintenance')
  const row = page.getByTestId('requests').locator('[data-testid^="request-"]').filter({ hasText: what }).first()
  await row.locator('[data-testid^="select-"]').click()
  const detail = page.getByTestId('detail')
  // STEP 5: Ditugaskan ke → Simpan penugasan
  await pickCombo(detail, 'assign_to', ROLES.housekeeping.fullName)
  await detail.getByTestId('assign').click()
  await expect(page.getByTestId('notice')).toBeVisible()
  // STEP 6: Mulai dikerjakan
  await detail.getByTestId('start').click()
  await expect(detail.getByTestId('resolve')).toBeVisible()
  // STEP 7: out of sale until the day after tomorrow
  const block = detail.getByTestId('block-form')
  await block.locator('select[name=block_type]').selectOption('OOO')
  await setDate(block, 'block_end', addDays(w.businessDate, 2))
  await shot(detail, 'housekeeping-perawatan-07')
  await block.getByTestId('block-room').click()
  await expect(page.getByTestId('notice')).toBeVisible()
  // STEP 8: Selesaikan, with what was done; the block is released with it
  await detail.getByTestId('resolve').click()
  const close = page.getByTestId('close-form')
  await close.locator('input[name=note]').fill('Seal keran diganti')
  await shot(close, 'housekeeping-perawatan-08')
  await close.getByTestId('close-submit').click()
  await expect(page.getByTestId('notice')).toBeVisible()
}

/** 02-housekeeping/barang-tertinggal.md */
export async function flowBarangTertinggal(page: Page, loginAs: LoginAs, w: DocsWorld): Promise<void> {
  const lina = w.inHouse.lina
  const what = 'Kacamata hitam dalam kotak cokelat'
  // STEP 1: Kamar → Barang tertinggal → Catat barang (the housekeeper, in the room of Lina Kusuma)
  await loginAs('housekeeping', '/lost-found')
  await page.getByTestId('new-item').click()
  const form = page.getByTestId('item-form')
  // STEP 2: the form
  await pickCombo(form, 'room_id', `${lina.room} ·`)
  await form.locator('select[name=category]').selectOption('OTHER')
  await form.locator('input[name=storage_location]').fill('Brankas FO')
  await form.locator('input[name=description]').fill(what)
  await form.locator('input[name=possible_owner]').fill(`${lina.guest.first_name} ${lina.guest.last_name}`)
  await shot(form, 'housekeeping-barang-02')
  // STEP 3: Catat
  await form.getByRole('button', { name: tx('lostFound.recordButton'), exact: true }).click()
  await expect(page.getByTestId('notice')).toBeVisible()

  // STEP 4: the duty manager finds the item
  await loginAs('admin', '/lost-found')
  const q = page.getByTestId('filters').locator('input[name=q]')
  await q.fill('Kacamata')
  await q.press('Enter')
  const row = page.getByTestId('items').locator('[data-testid^="item-"]').filter({ hasText: what }).first()
  await row.locator('[data-testid^="select-"]').click()
  // STEP 5: who stayed in the room
  const detail = page.getByTestId('detail')
  await expect(detail.getByTestId('owners')).toContainText(lina.guest.last_name)
  await shot(detail, 'housekeeping-barang-05', { blur: ['[data-testid=owners] small'] }) // the guest's phone and e-mail
  // STEP 6: Serahkan ke tamu ini
  await detail.getByTestId(`owner-${lina.stayNumber}`).click()
  // STEP 7: the hand-over
  const ret = page.getByTestId('return-form')
  await ret.locator('input[name=claimant_proof]').fill('KTP dicocokkan, isi kotak disebutkan dengan benar')
  await ret.getByTestId('return-submit').click()
  await expect(page.getByTestId('notice')).toBeVisible()
}

// =====================================================================================================================
// 03-kasir-dan-pembayaran

/** Signs the approval dialog of a correction with the credentials of a user (the supervisor approves her own: the rule of the property is the default FREE). */
async function approve(page: Page, email: string, password: string, file?: string): Promise<void> {
  const dialog = page.getByTestId('approval-dialog')
  await expect(dialog).toBeVisible()
  await dialog.locator('input[name=approval_email]').fill(email)
  await dialog.locator('input[name=approval_password]').fill(password)
  if (file) await shot(dialog, file)
  await dialog.getByTestId('approval-submit').click()
  await expect(dialog).toHaveCount(0)
}

/** The balance of the folio on screen as a whole number (negative when the folio holds a credit). */
async function folioBalance(page: Page): Promise<number> {
  const text = await page.getByTestId('balance').first().innerText()
  const digits = Number(text.replace(/[^\d]/g, '') || '0')
  return /[-−]/.test(text) ? -digits : digits
}

/** The visible row of a folio line by its text (each row is drawn twice: a table row and a phone card). */
function folioRow(page: Page, text: string): Locator {
  return page.getByTestId('items').locator('[data-testid^="item-"]:visible').filter({ hasText: text }).last()
}

/** 03-kasir-dan-pembayaran/posting-biaya-ke-folio.md */
export async function flowPostingBiaya(page: Page, loginAs: LoginAs, w: DocsWorld): Promise<void> {
  // STEP 1: the folio of Agus Pratama
  await loginAs('frontdesk', `/folios/${w.inHouse.agus.folioId}`)
  // STEP 2: the tab Biaya
  await page.getByTestId('tab-charge').click()
  // STEP 3: the form Posting biaya
  const form = page.getByTestId('charge-form')
  await pickCombo(form, 'charge_code', 'LAUNDRY ·')
  await form.locator('input[name=quantity]').fill('2')
  await form.locator('input[name=unit_price]').fill('35000')
  await form.locator('input[name=description]').fill('Laundry 2 kemeja, nota 0457')
  await shot(form, 'kasir-biaya-03')
  // STEP 4: Posting biaya: the new line in the list
  await form.getByRole('button', { name: tx('folio.postCharge'), exact: true }).click()
  await expect(page.getByTestId('notice')).toBeVisible()
  await expect(folioRow(page, 'nota 0457')).toBeVisible()
  await shotAround([page.getByTestId('notice'), page.getByTestId('items')], 'kasir-biaya-04')
}

/** 03-kasir-dan-pembayaran/menerima-pembayaran.md */
export async function flowPembayaran(page: Page, loginAs: LoginAs, w: DocsWorld): Promise<void> {
  // STEP 1-2: the folio of Agus Pratama, the tab Pembayaran
  await loginAs('frontdesk', `/folios/${w.inHouse.agus.folioId}?tab=payment`)
  // STEP 3: the form Terima pembayaran
  const form = page.getByTestId('payment-form')
  await form.locator('input[name=amount]').fill('500000')
  await form.locator('select[name=payment_method]').selectOption('CARD')
  await form.locator('input[name=reference]').fill('EDC 004512')
  await shot(form, 'kasir-pembayaran-03')
  // STEP 4: Posting pembayaran
  await form.getByRole('button', { name: tx('folio.postPayment'), exact: true }).click()
  await expect(page.getByTestId('notice')).toBeVisible()
  // STEP 5: the payment line with its button Kuitansi
  const row = folioRow(page, '(Kartu)')
  await expect(row.locator('[data-testid^="receipt-"]')).toBeVisible()
  await shot(page.getByTestId('items'), 'kasir-pembayaran-05')
}

/** 03-kasir-dan-pembayaran/reverse-dan-penyesuaian.md */
export async function flowKoreksi(page: Page, loginAs: LoginAs, w: DocsWorld, seed: SeedState): Promise<void> {
  const sari = seed.users.supervisor
  // STEP 1-2: the laundry posted today on the folio of Agus Pratama: Reverse
  await loginAs('supervisor', `/folios/${w.inHouse.agus.folioId}`)
  await folioRow(page, 'nota 0457').locator('[data-testid^="reverse-"]').click()
  // STEP 3: the reason, then Lanjut ke persetujuan
  const form = page.getByTestId('correction-form')
  await form.locator('input[name=reason]').fill('Salah kamar, seharusnya kamar 305')
  await shot(form, 'kasir-koreksi-03')
  await form.getByRole('button', { name: tx('folio.continueApproval'), exact: true }).click()
  // STEP 4: Setujui reversal
  await approve(page, sari.email, sari.password)
  await expect(page.getByTestId('notice')).toBeVisible()

  // STEP 5: the tab Penyesuaian
  await page.getByTestId('tab-adjust').click()
  // STEP 6: a credit on the room charge of last night
  const adjust = page.getByTestId('adjust-form')
  await pickCombo(adjust, 'adjust_code', 'ROOM ·')
  await adjust.locator('input[name=adjust_amount]').fill('-100000')
  await adjust.locator('input[name=adjust_reason]').fill('Kompensasi AC kamar tidak dingin semalam')
  await shot(adjust, 'kasir-koreksi-06')
  // STEP 7: Lanjut ke persetujuan → Setujui penyesuaian
  await adjust.getByRole('button', { name: tx('folio.continueApproval'), exact: true }).click()
  await approve(page, sari.email, sari.password)
  await expect(page.getByTestId('notice')).toBeVisible()
}

/** 03-kasir-dan-pembayaran/void-dan-refund.md */
export async function flowVoidRefund(page: Page, loginAs: LoginAs, w: DocsWorld, seed: SeedState): Promise<void> {
  const sari = seed.users.supervisor
  const made = w.inHouse.made
  // (the money of the example: a QRIS payment taken by mistake, and a card payment above the balance that leaves a credit)
  await loginAs('supervisor', `/folios/${made.folioId}?tab=payment`)
  const owed = await folioBalance(page)
  const pay = page.getByTestId('payment-form')
  for (const [amount, method, ref] of [['150000', 'QRIS', 'QRIS salah input'], [String(Math.max(owed, 0) + 250000), 'CARD', 'EDC 009911']] as const) {
    await pay.locator('input[name=amount]').fill(amount)
    await pay.locator('select[name=payment_method]').selectOption(method)
    await pay.locator('input[name=reference]').fill(ref)
    await pay.getByRole('button', { name: tx('folio.postPayment'), exact: true }).click()
    await expect(page.getByTestId('notice')).toBeVisible()
  }
  // STEP 1-2: Void on the payment of today
  await folioRow(page, '(QRIS)').locator('[data-testid^="void-"]').click()
  // STEP 3: the reason
  const form = page.getByTestId('correction-form')
  await form.locator('input[name=reason]').fill('Salah metode: tamu membayar dengan kartu, bukan QRIS')
  await form.getByRole('button', { name: tx('folio.continueApproval'), exact: true }).click()
  // STEP 4: Setujui void
  await approve(page, sari.email, sari.password)
  await expect(page.getByTestId('notice')).toBeVisible()

  // a cash refund goes out of the supervisor's own drawer: her shift is opened first (Shift kasir)
  await page.goto('/cashier/shifts')
  const open = page.getByTestId('open-form')
  await open.locator('input[name=drawer]').fill('FO2')
  await open.locator('input[name=opening_float]').fill('500000')
  await open.getByTestId('open-shift').click()
  await expect(page.getByTestId('current-shift')).toBeVisible()
  await page.goto(`/folios/${made.folioId}`)
  // STEP 5: Refund on the card payment
  await folioRow(page, '(Kartu)').locator('[data-testid^="refund-"]').click()
  // STEP 6: the credit of the folio goes back in cash
  const refund = page.getByTestId('correction-form')
  await refund.locator('input[name=refund_amount]').fill('250000')
  await refund.locator('input[name=refund_reference]').fill('Diserahkan tunai ke tamu')
  await refund.locator('input[name=reason]').fill('Kelebihan bayar dikembalikan')
  await shot(refund, 'kasir-void-refund-06')
  // STEP 7: Setujui refund
  await refund.getByRole('button', { name: tx('folio.continueApproval'), exact: true }).click()
  await approve(page, sari.email, sari.password, 'kasir-void-refund-07')
  await expect(page.getByTestId('notice')).toBeVisible()
  // the supervisor's drawer is closed again with the cash that is left
  await page.goto('/cashier/shifts')
  await page.getByTestId('start-close').click()
  const close = page.getByTestId('close-form')
  const expected = (await page.getByTestId('expected').innerText()).replace(/[^\d]/g, '')
  await close.locator('input[name=counted_cash]').fill(expected)
  await close.getByTestId('confirm-close').click()
  await expect(page.getByTestId('closed-card')).toBeVisible()
}

/** 03-kasir-dan-pembayaran/transfer-antar-folio.md */
export async function flowTransferFolio(page: Page, loginAs: LoginAs, w: DocsWorld, seed: SeedState): Promise<void> {
  const sari = seed.users.supervisor
  const { siti, andreas } = w.inHouse
  // (the minibar of Siti Rahmawati's room, which Andreas Wijaya pays for)
  await loginAs('supervisor', `/folios/${siti.folioId}`)
  await page.getByTestId('tab-charge').click()
  const charge = page.getByTestId('charge-form')
  await pickCombo(charge, 'charge_code', 'MINIBAR ·')
  await charge.locator('input[name=quantity]').fill('2')
  await charge.locator('input[name=unit_price]').fill('45000')
  await charge.locator('input[name=description]').fill('Minibar: 2 bir kaleng')
  await charge.getByRole('button', { name: tx('folio.postCharge'), exact: true }).click()
  await expect(page.getByTestId('notice')).toBeVisible()
  // STEP 1: the minibar line ticked
  const row = folioRow(page, '2 bir kaleng')
  await row.locator('[data-testid^="select-"]').check()
  await expect(page.getByTestId('selection-summary')).toBeVisible()
  await shotAround([page.getByTestId('transfer-bar'), row], 'kasir-transfer-01')
  // STEP 2: Transfer…
  await page.getByTestId('transfer-items-open').click()
  const dialog = page.getByTestId('transfer-dialog')
  // STEP 3: Folio tujuan and Alasan
  await pickCombo(dialog, 'transfer_target', andreas.guest.last_name)
  await dialog.locator('input[name=transfer_reason]').fill(`Minibar kamar ${siti.room} ditanggung tamu kamar ${andreas.room}`)
  await expect(dialog.getByTestId('target-scope')).toBeVisible()
  await shot(dialog, 'kasir-transfer-03')
  // STEP 4: Lanjut ke persetujuan → Setujui transfer
  await dialog.getByTestId('transfer-continue').click()
  await approve(page, sari.email, sari.password)
  await expect(page.getByTestId('notice')).toBeVisible()
}

/** 03-kasir-dan-pembayaran/grup-tagihan-dan-cetak.md */
export async function flowGrupTagihan(page: Page, loginAs: LoginAs, w: DocsWorld): Promise<void> {
  // STEP 1: the folio of Agus Pratama
  await loginAs('frontdesk', `/folios/${w.inHouse.agus.folioId}`)
  // STEP 2: the card payment into Grup B
  const row = folioRow(page, '(Kartu)')
  await row.locator('select[name^="group_"]').selectOption('B')
  await expect(page.getByTestId('group-B')).toContainText('1')
  await shot(page.getByTestId('items'), 'kasir-grup-02')
  // STEP 3: Grup B alone, then all again
  await page.getByTestId('group-B').click()
  await expect(page.getByTestId('group-note')).toBeVisible()
  await page.getByTestId('group-ALL').click()
  // STEP 4-5: Grup yang dicetak and Cetak tagihan (the picture only: the PDF opens in a new tab)
  await page.getByTestId('print-group').selectOption('B')
  await shot(page.getByTestId('summary'), 'kasir-grup-05')
}

/** 03-kasir-dan-pembayaran/shift-kasir.md */
export async function flowShiftKasir(page: Page, loginAs: LoginAs, w: DocsWorld, seed: SeedState): Promise<void> {
  const sari = seed.users.supervisor
  // STEP 1: Tagihan → Shift kasir (the cashier Kevin)
  await loginAs('cashier', '/cashier/shifts')
  // STEP 2: Laci and Modal awal
  const open = page.getByTestId('open-form')
  await open.locator('input[name=drawer]').fill('FO1')
  await open.locator('input[name=opening_float]').fill('1000000')
  await shot(open, 'kasir-shift-02')
  // STEP 3: Buka shift
  await open.getByTestId('open-shift').click()
  await expect(page.getByTestId('current-shift')).toBeVisible()
  // STEP 4: a cash payment of the guest of room of Agus Pratama
  await page.goto(`/folios/${w.inHouse.agus.folioId}?tab=payment`)
  const pay = page.getByTestId('payment-form')
  await pay.locator('input[name=amount]').fill('100000')
  await pay.locator('select[name=payment_method]').selectOption('CASH')
  await pay.getByRole('button', { name: tx('folio.postPayment'), exact: true }).click()
  await expect(page.getByTestId('notice')).toBeVisible()
  // STEP 7-8: Tutup shift… with 20 000 short
  await page.goto('/cashier/shifts')
  const expected = Number((await page.getByTestId('expected').innerText()).replace(/[^\d]/g, ''))
  await page.getByTestId('start-close').click()
  const close = page.getByTestId('close-form')
  await close.locator('input[name=counted_cash]').fill(String(expected - 20000))
  await close.locator('input[name=reason]').fill('Kembalian kurang Rp20.000 ke tamu kamar 204')
  await expect(close.getByTestId('difference')).toBeVisible()
  await shot(close, 'kasir-shift-08')
  // STEP 9: Tutup shift → Setujui selisih by the supervisor
  await close.getByTestId('confirm-close').click()
  await approve(page, sari.email, sari.password)
  // STEP 10: the shift is closed, with its Z report
  await expect(page.getByTestId('closed-card')).toBeVisible()
  await shotAround([page.getByTestId('notice'), page.getByTestId('closed-card')], 'kasir-shift-10')
}

/** 03-kasir-dan-pembayaran/posting-biaya-kamar.md */
export async function flowBiayaKamar(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1: Tagihan → Biaya kamar
  await loginAs('frontdesk', '/room-charges')
  await expect(page.getByTestId('items')).toBeVisible()
  await shotAround([page.locator('main h1'), page.getByTestId('items')], 'kasir-biaya-kamar-01')
  // STEP 2-3: Posting biaya kamar
  await expect(page.getByTestId('ready-count')).toBeVisible()
  await page.getByTestId('post').click()
  // STEP 4: the outcome
  await expect(page.getByTestId('outcome')).toBeVisible()
}

/** 03-kasir-dan-pembayaran/membuka-folio-untuk-outlet.md */
export async function flowFolioOutlet(page: Page, loginAs: LoginAs, w: DocsWorld): Promise<void> {
  // STEP 1: the folio of Made Wirawan (the duty manager: the seeded desks may not open folios for outlets)
  await loginAs('admin', `/folios/${w.inHouse.made.folioId}`)
  const card = page.locator('[data-testid=folio-outlet-lock], [data-testid=outlet-lock-card]').first()
  await expect(card.getByTestId('lock-status')).toBeVisible()
  // STEP 2: limit, until check-out, the restaurant and the bar
  await card.locator('input[name=outlet_limit]').fill('2000000')
  await card.getByTestId('lock-mode-until').check()
  await card.getByTestId('lock-outlet-RESTO').check()
  await card.getByTestId('lock-outlet-BAR').check()
  await shot(card, 'kasir-folio-outlet-02')
  // STEP 3: Buka untuk outlet
  await card.getByTestId('lock-save').click()
  await expect(card.getByTestId('lock-open-summary')).toBeVisible()
  await shot(card, 'kasir-folio-outlet-03')
}

// =====================================================================================================================
// 04-pos

/** Chooses an outlet in a <select> by a part of its name (the ids differ between runs). */
async function chooseOutlet(select: Locator, name: string): Promise<void> {
  const value = await select.locator('option', { hasText: name }).first().getAttribute('value')
  await select.selectOption(value ?? '')
}

const RESTO = 'Teratai'

/** Adds a product to the open bill: its category tab first (the menu shows one category at a time), then the product. */
async function addProduct(page: Page, category: string, product: RegExp): Promise<void> {
  const menu = page.getByTestId('menu')
  await menu.getByRole('tab', { name: category, exact: true }).click()
  await menu.getByTestId('products').getByRole('button', { name: product }).first().click()
}

/** 04-pos/menyiapkan-outlet.md */
export async function flowMenyiapkanOutlet(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 2-4: POS → Outlet: the outlets of the hotel (the demo of pms-seed pos)
  await loginAs('admin', '/pos/outlets')
  await expect(page.locator('[data-testid^="outlet-"]').first()).toBeVisible()
  await shot(page.locator('main'), 'pos-outlet-03', { pad: 0 })
  // STEP 5-8: POS → Stasiun dan printer: what the set-up still misses
  await page.goto('/pos/stations')
  const check = page.getByTestId('setup-check')
  await expect(check).toBeVisible()
  await shot(check, 'pos-outlet-08')
}

/** 04-pos/mengambil-pesanan.md */
export async function flowAmbilPesanan(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1: POS → Denah meja dan bill, the restaurant
  await loginAs('pos', '/pos/floor')
  await chooseOutlet(page.getByTestId('outlet-select'), RESTO)
  await expect(page.getByTestId('table-3')).toBeVisible()
  await shot(page.locator('main'), 'pos-pesanan-01', { pad: 0 })
  // STEP 2: table 3 → Jumlah tamu → Bill baru
  await page.getByTestId('table-3').click()
  const table = page.getByTestId('table-dialog')
  await table.getByTestId('guest-count').fill('2')
  await table.getByTestId('open-table').click()
  await expect(page).toHaveURL(/\/pos\/bills\/\d+$/)
  // STEP 3-4: a fried rice with its choices, and an iced tea
  await addProduct(page, 'Makanan utama', /Nasi Goreng Kampung/)
  const options = page.getByTestId('options-dialog')
  await options.getByTestId('options-note').fill('tanpa bawang')
  await options.getByTestId('options-add').click()
  await addProduct(page, 'Minuman', /Es Teh Manis/)
  // STEP 5: the lines not sent yet
  await expect(page.getByTestId('draft-lines')).toContainText('Es Teh Manis')
  await shot(page.getByTestId('bill-panel'), 'pos-pesanan-05')
  // STEP 6: Kirim ke dapur dan bar
  await page.getByTestId('send').click()
  await expect(page.getByTestId('round-1')).toBeVisible()
  await shot(page.getByTestId('bill-panel'), 'pos-pesanan-06')
}

/** 04-pos/layar-dapur.md */
export async function flowLayarDapur(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1: POS → Layar dapur, the main kitchen
  await loginAs('pos', '/pos/kds')
  await page.locator('[data-testid^="station-"]').filter({ hasText: 'Dapur utama' }).first().click()
  // STEP 2: the ticket of table 3 in Baru
  const ticket = page.getByTestId('col-NEW').locator('[data-testid^="ticket-"]').filter({ hasText: 'Meja 3' }).first()
  await expect(ticket).toBeVisible()
  await shot(page.getByTestId('kds'), 'pos-dapur-02', { pad: 0 })
  const id = ((await ticket.getAttribute('data-testid')) ?? '').replace('ticket-', '')
  // STEP 3: Mulai
  await page.getByTestId(`next-${id}`).click()
  await expect(page.getByTestId('col-COOKING').getByTestId(`ticket-${id}`)).toBeVisible()
  // STEP 4: Siap
  await page.getByTestId(`next-${id}`).click()
  await expect(page.getByTestId('col-READY').getByTestId(`ticket-${id}`)).toBeVisible()
  // STEP 5: Sudah diambil
  await page.getByTestId(`next-${id}`).click()
  await expect(page.getByTestId(`ticket-${id}`)).toHaveCount(0)
}

/** Leaves the pay dialog with one row of the method (the dialog opens with a cash row for the whole bill). */
async function onlyMethod(pay: Locator, method: 'CARD' | 'ROOM'): Promise<void> {
  await expect(pay.locator('[data-testid^=pay-row-]').first()).toBeVisible()
  const removes = pay.locator('[data-testid^=remove-]')
  for (let n = await removes.count(); n > 0; n--) {
    await removes.first().click()
    await expect(removes).toHaveCount(n - 1)
  }
  await pay.getByTestId(`add-${method}`).click()
  await expect(pay.getByTestId(`pay-row-${method}`)).toBeVisible()
}

/** 04-pos/membayar-bill.md */
export async function flowBayarBill(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1: the bill of table 3
  await loginAs('pos', '/pos/floor')
  await chooseOutlet(page.getByTestId('outlet-select'), RESTO)
  await page.getByTestId('table-3').click()
  await expect(page).toHaveURL(/\/pos\/bills\/\d+$/)
  // STEP 2-3: Bayar, by card for the whole bill
  await page.getByTestId('pay').click()
  const pay = page.getByTestId('pay-dialog')
  await onlyMethod(pay, 'CARD')
  await pay.getByTestId('exact-CARD').click()
  await pay.getByTestId('pay-row-CARD').locator('input[name=reference]').fill('EDC 118822')
  // STEP 4: Dimasukkan, Sisa
  await expect(pay.getByTestId('pay-summary')).toBeVisible()
  await shot(pay, 'pos-bayar-03')
  // STEP 5: Bayar dan tutup
  await pay.getByTestId('pay-submit').click()
  await expect(page.getByTestId('bill-closed')).toBeVisible()
  await shot(page.getByTestId('bill-panel'), 'pos-bayar-05')
}

/** 04-pos/bebankan-ke-kamar.md */
export async function flowBebankanKamar(page: Page, loginAs: LoginAs, w: DocsWorld): Promise<void> {
  const made = w.inHouse.made
  // (a room service order of Made Wirawan, whose folio is open for the restaurant: membuka-folio-untuk-outlet)
  await loginAs('pos', '/pos/floor')
  await chooseOutlet(page.getByTestId('outlet-select'), RESTO)
  await page.getByTestId('new-takeaway').click()
  const take = page.getByTestId('takeaway-dialog')
  await take.locator('select[name=order_type]').selectOption('ROOM_SERVICE')
  await take.locator('input[name=customer_name]').fill(`${made.guest.first_name} ${made.guest.last_name}`)
  await take.getByTestId('open-takeaway').click()
  await expect(page).toHaveURL(/\/pos\/bills\/\d+$/)
  await addProduct(page, 'Minuman', /Kopi Bali/)
  await page.getByTestId('send').click()
  await expect(page.getByTestId('round-1')).toBeVisible()
  // STEP 1-2: Bayar → + Bebankan ke kamar
  await page.getByTestId('pay').click()
  const pay = page.getByTestId('pay-dialog')
  await onlyMethod(pay, 'ROOM')
  // STEP 3: the room number → Cari
  await pay.getByTestId('room-query').fill(made.room)
  await pay.getByTestId('room-find').click()
  // STEP 4: the guest, with the state of the folio
  const result = pay.getByTestId(`room-${made.stayId}`)
  await expect(result).toBeVisible()
  await shot(pay, 'pos-kamar-04')
  await result.click()
  await expect(pay.getByTestId('room-chosen')).toContainText(made.room)
  // STEP 5: Pas → Bayar dan tutup
  await pay.getByTestId('exact-ROOM').click()
  await pay.getByTestId('pay-submit').click()
  await expect(page.getByTestId('bill-closed')).toBeVisible()
  await expect(page.getByTestId('room-charges')).toContainText(made.room)
}

/** 04-pos/void-diskon-komplimen.md */
export async function flowVoidPos(page: Page, loginAs: LoginAs, seed: SeedState): Promise<void> {
  const admin = seed.users.admin
  // (a table of the restaurant with a satay and an avocado juice sent to the kitchen; the duty manager holds void and approval)
  await loginAs('admin', '/pos/floor')
  await chooseOutlet(page.getByTestId('outlet-select'), RESTO)
  await page.getByTestId('table-5').click()
  const table = page.getByTestId('table-dialog')
  await table.getByTestId('guest-count').fill('2')
  await table.getByTestId('open-table').click()
  await expect(page).toHaveURL(/\/pos\/bills\/\d+$/)
  await addProduct(page, 'Makanan utama', /Sate Ayam Madura/)
  await addProduct(page, 'Minuman', /Jus Alpukat/)
  await page.getByTestId('send').click()
  await expect(page.getByTestId('round-1')).toBeVisible()
  // STEP 1: Void on the juice
  await page.getByTestId('round-1').locator('li').filter({ hasText: 'Jus Alpukat' }).locator('[data-testid^="void-"]').click()
  // STEP 2: the reason → Lanjut ke persetujuan
  const sheet = page.getByTestId('reason-dialog')
  await sheet.getByTestId('reason').fill('Tamu membatalkan, belum dibuat')
  await shot(sheet, 'pos-void-02')
  await sheet.getByTestId('reason-submit').click()
  // STEP 3: the approval, then the voided line on the bill
  await approve(page, admin.email, admin.password)
  await expect(page.getByTestId('void-lines')).toContainText('Jus Alpukat')
  await shot(page.getByTestId('bill-panel'), 'pos-void-03')
  // the table is paid by card, so no open bill is left
  await page.getByTestId('pay').click()
  const pay = page.getByTestId('pay-dialog')
  await onlyMethod(pay, 'CARD')
  await pay.getByTestId('exact-CARD').click()
  await pay.getByTestId('pay-submit').click()
  await expect(page.getByTestId('bill-closed')).toBeVisible()
}

/** 04-pos/laporan-outlet.md */
export async function flowLaporanOutlet(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1-2: POS → Laporan outlet, the restaurant, today
  await loginAs('pos', '/pos/report')
  await chooseOutlet(page.getByTestId('report-outlet'), RESTO)
  // STEP 3: the report
  await expect(page.getByTestId('report-totals')).toBeVisible()
  await expect(page.getByTestId('report-methods')).toBeVisible()
  await shotAround([page.locator('main h1'), page.getByTestId('report-methods')], 'pos-laporan-03')
}

// =====================================================================================================================
// 07-accounting

/** 07-accounting/bagan-akun-dan-akun-sistem.md */
export async function flowBaganAkun(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1: Akuntansi → Bagan akun → Akun baru (Andi Akuntansi)
  await loginAs('accounting', '/accounting/accounts')
  await page.getByTestId('new-account').click()
  // STEP 2: a petty cash account under 1100
  const form = page.getByTestId('account-form')
  await form.locator('input[name=code]').fill('1125')
  await form.locator('input[name=name]').fill('Kas kecil housekeeping')
  await form.locator('select[name=account_type]').selectOption('ASSET')
  await pickCombo(form, 'parent_id', '1100 ·')
  await form.locator('select[name=statement_group]').selectOption('CASH')
  await form.locator('input[name=description]').fill('Kas kecil yang dipegang kepala housekeeping')
  await shot(form, 'akuntansi-bagan-02')
  // STEP 3: Simpan
  await form.getByRole('button', { name: tx('common.save'), exact: true }).click()
  await expect(form).toHaveCount(0)
  // STEP 4: Cari the code
  await page.locator('input[name=q]').first().fill('112')
  await expect(page.getByTestId('account-1125')).toBeVisible()
  await shot(page.getByTestId('accounts'), 'akuntansi-bagan-04')
  // STEP 7-8: Akuntansi → Akun sistem
  await page.goto('/accounting/mapping')
  await expect(page.getByTestId('map-CITY_LEDGER')).toBeVisible()
  await shotAround([page.locator('main h1'), page.getByTestId('map-CITY_LEDGER')], 'akuntansi-sistem-07')
}

/** 07-accounting/jurnal-manual-dan-pembalikan.md */
export async function flowJurnalManual(page: Page, loginAs: LoginAs, w: DocsWorld, seed: SeedState): Promise<void> {
  const admin = seed.users.admin
  // STEP 1: Akuntansi → Jurnal → Jurnal baru
  await loginAs('accounting', '/accounting/journals')
  await page.getByTestId('new-journal').click()
  const form = page.getByTestId('journal-form')
  // STEP 2: the header
  await setDate(form, 'date', w.businessDate)
  await form.locator('input[name=reference]').fill('BKK-0112')
  await form.locator('input[name=description]').fill('Pengisian kas kecil dari bank')
  // STEP 3: two lines, balanced
  await pickCombo(form.getByTestId('line-0'), 'account_0', '1120 ·')
  await form.locator('input[name=debit_0]').fill('500000')
  await pickCombo(form.getByTestId('line-1'), 'account_1', '1130 ·')
  await form.locator('input[name=credit_1]').fill('500000')
  await expect(form.getByTestId('line-totals')).toContainText(tx('journals.balanced'))
  await shot(form, 'akuntansi-jurnal-03')
  // STEP 4: Posting
  await form.getByTestId('journal-post').click()
  const notice = page.getByTestId('notice')
  await expect(notice).toBeVisible()
  const number = ((await notice.innerText()).match(/JV\d+/) ?? [''])[0]
  // STEP 5-6: the journal, Balik…, the reason
  await page.getByTestId(`journal-${number}`).click()
  const detail = page.getByTestId('journal-detail')
  await detail.getByTestId('reverse').click()
  await detail.locator('input[name=reason]').fill('Seharusnya rekening penggajian')
  await shot(detail, 'akuntansi-jurnal-06')
  // STEP 7: Balik dengan persetujuan → Setujui (the accountant may not approve: the duty manager does)
  await detail.getByTestId('reverse-ask').click()
  await approve(page, admin.email, admin.password)
  // STEP 8: the reversed journal in the list
  await expect(page.getByTestId(`journal-${number}`)).toContainText(number)
  await expect(notice).toContainText(number)
  await shotAround([notice, page.getByTestId(`journal-${number}`)], 'akuntansi-jurnal-08')
}

/** 07-accounting/periode-dan-tahun-buku.md */
export async function flowPeriode(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1: Akuntansi → Periode akuntansi
  await loginAs('admin', '/accounting/periods')
  await expect(page.getByTestId('periods')).toBeVisible()
  await shotAround([page.locator('main h1'), page.getByTestId('periods')], 'akuntansi-periode-01')
  // STEP 5: Akuntansi → Tahun buku
  await page.goto('/accounting/fiscal-years')
  await expect(page.getByTestId('years')).toBeVisible()
  await shotAround([page.locator('main h1'), page.getByTestId('years')], 'akuntansi-periode-05')
}

/** 07-accounting/departemen.md */
export async function flowDepartemen(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1-2: Akuntansi → Departemen → Tambah sub-departemen on FB
  await loginAs('accounting', '/accounting/departments')
  await page.getByTestId('sub-FB').click()
  const form = page.getByTestId('add-form')
  await form.locator('input[name=code]').fill('BAR')
  await form.locator('input[name=name]').fill('Bar kolam')
  await form.locator('input[name=sort_order]').fill('21')
  await shot(form, 'akuntansi-departemen-02')
  // STEP 3: Buat
  await form.getByTestId('create').click()
  await expect(page.getByTestId('notice')).toBeVisible()
  // STEP 4: the list with the new sub-department
  await expect(page.getByTestId('department-BAR')).toBeVisible()
  await shot(page.getByTestId('departments'), 'akuntansi-departemen-04')
  // STEP 5-7: Akuntansi → Laporan departemen
  await page.goto('/accounting/department-report')
  await expect(page.getByTestId('report')).toBeVisible()
  await shotAround([page.locator('main h1'), page.getByTestId('report')], 'akuntansi-departemen-07')
}

/** 07-accounting/laporan-keuangan.md */
export async function flowLaporanKeuangan(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1-2: Akuntansi → Neraca saldo
  await loginAs('accounting', '/accounting/trial-balance')
  await expect(page.getByTestId('trial-balance')).toBeVisible()
  await shotAround([page.locator('main h1'), page.getByTestId('trial-balance')], 'akuntansi-laporan-02')
  // STEP 4: Akuntansi → Laporan laba rugi
  await page.goto('/accounting/income-statement')
  await expect(page.getByTestId('statement')).toBeVisible()
  await shotAround([page.locator('main h1'), page.getByTestId('statement')], 'akuntansi-laporan-04')
  // STEP 8-9: Akuntansi → Akun kontrol, the last closed day
  await page.goto('/accounting/reconciliation')
  await page.getByTestId('apply').click()
  await expect(page.getByTestId('controls')).toBeVisible()
  await shotAround([page.locator('main h1'), page.getByTestId('controls')], 'akuntansi-laporan-06')
}

// --- 05-banquet. No role of the E2E seed holds a banquet permission: the pictures use the administrator. The seed has rooms, items, the
// FULLDAY package and the STANDARD template but no event, so the main event is made on screen (session on the business date, so that it can
// be posted) and the event of the cancellation picture by the API.

const BQ_EVENT = 'Rapat Kerja Tahunan'
const BQ_COMPANY = 'PT Cahaya Nusantara'

/** Selects the option of a select whose text contains `text`. */
async function selectContaining(select: Locator, text: string): Promise<void> {
  const value = await select.locator('option', { hasText: text }).first().getAttribute('value')
  await select.selectOption(value ?? '')
}

async function bqEvent(data: Factories, name: string): Promise<{ id: number; number: string; sessions: { id: number }[] }> {
  const list = await data.api.all<{ id: number; name: string }>(`${data.P}/banquet/events`)
  const ev = list.find((e) => e.name === name)
  if (!ev) throw new Error(`banquet event ${name} not found`)
  return data.api.get(`${data.P}/banquet/events/${ev.id}`)
}

export async function flowMenyiapkanBanquet(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1-2: Banquet → Ruang pertemuan
  await loginAs('admin', '/banquet/rooms')
  await expect(page.getByTestId('room-SARA')).toBeVisible()
  await shotAround([page.locator('main h1'), page.getByTestId('room-list')], 'banquet-setup-02')
  // STEP 5: Item dan paket, tab Paket
  await page.goto('/banquet/catalog')
  await page.getByTestId('tab-packages').click()
  await expect(page.getByTestId('package-FULLDAY')).toBeVisible()
  await shotAround([page.locator('main h1'), page.getByTestId('package-FULLDAY')], 'banquet-setup-05')
  // STEP 6: Pengaturan banquet, the policy templates
  await page.goto('/banquet/settings')
  await expect(page.getByTestId('template-STANDARD')).toBeVisible()
  await shot(page.getByTestId('templates-section'), 'banquet-setup-06')
}

export async function flowAcaraBaru(page: Page, loginAs: LoginAs, w: DocsWorld, data: Factories): Promise<void> {
  const company = await data.company({ name: BQ_COMPANY })
  // STEP 1-2: Banquet → Acara → Pemesanan baru
  await loginAs('admin', '/banquet/events')
  await page.getByTestId('event-new').click()
  const ev = page.getByTestId('banquet-event-dialog')
  await ev.locator('input[name=name]').fill(BQ_EVENT)
  await ev.locator('select[name=booker_company_id]').selectOption(String(company.id))
  await ev.locator('input[name=contact_name]').fill('Ibu Rina Kartika')
  await shot(ev, 'banquet-acara-02')
  // STEP 3: Simpan
  await page.getByTestId('banquet-event-save').click()
  await expect(page).toHaveURL(/\/banquet\/events\/\d+/)
  await expect(page.getByTestId('event-status')).toHaveText(tx('banquet.status.INQUIRY'))
  // STEP 4-5: Tambah sesi, on the business date so that it can be posted later
  await page.getByTestId('session-add').click()
  const s = page.getByTestId('banquet-session-dialog')
  await s.locator('input[name=name]').fill('Rapat hari pertama')
  await selectContaining(page.getByTestId('session-room'), 'Saraswati A')
  await setDate(s, 'session_date', w.businessDate)
  await s.locator('input[name=start_time]').fill('09:00')
  await s.locator('input[name=end_time]').fill('17:00')
  await s.locator('input[name=pax_estimated]').fill('45')
  await s.locator('input[name=pax_guaranteed]').fill('40')
  await expect(page.getByTestId('check-free')).toBeVisible()
  await shot(s, 'banquet-acara-05')
  // STEP 6: Simpan
  await page.getByTestId('banquet-session-save').click()
  await expect(page.getByTestId('banquet-session-dialog')).toBeHidden()
  await expect(page.getByTestId('session-list')).toContainText('Rapat hari pertama')
  await shotAround([page.getByTestId('event-status'), page.getByTestId('session-list')], 'banquet-acara-06')
}

export async function flowTentatifKonfirmasi(page: Page, loginAs: LoginAs, w: DocsWorld, data: Factories): Promise<void> {
  const ev = await bqEvent(data, BQ_EVENT)
  // STEP 1-2: Tahan sebagai tentatif
  await loginAs('admin', `/banquet/events/${ev.id}`)
  await page.getByTestId('event-tentative').click()
  const t = page.getByTestId('tentative-dialog')
  await setDate(t, 'option_until', w.businessDate)
  await shot(t, 'banquet-konfirmasi-02')
  // STEP 3: Simpan
  await page.getByTestId('tentative-save').click()
  await expect(page.getByTestId('toast-success').first()).toBeVisible()
  await expect(page.getByTestId('event-status')).toHaveText(tx('banquet.status.TENTATIVE'))
  // STEP 4-5: Konfirmasi, the default template
  await page.getByTestId('event-confirm').click()
  const c = page.getByTestId('banquet-confirm-dialog')
  await expect(c.getByTestId('confirm-template')).toBeVisible()
  await shot(c, 'banquet-konfirmasi-05')
  // STEP 6: Konfirmasi
  await page.getByTestId('confirm-event').click()
  await expect(page.getByTestId('event-status')).toHaveText(tx('banquet.status.DEFINITE'))
  // STEP 7: Banquet → Kalender on the day of the event
  await page.goto('/banquet')
  await expect(page.getByTestId('calendar-day')).toBeVisible()
  await shotAround([page.getByTestId('calendar-bar'), page.getByTestId('calendar-day')], 'banquet-konfirmasi-07')
}

export async function flowItemDeposit(page: Page, loginAs: LoginAs, data: Factories): Promise<void> {
  const ev = await bqEvent(data, BQ_EVENT)
  const pkgs = await data.api.all<{ id: number; code: string }>(`${data.P}/banquet/packages`)
  const fullday = pkgs.find((p) => p.code === 'FULLDAY')!
  // STEP 1-2: tab Item & paket → Jual item atau paket, the package Fullday Meeting
  await loginAs('admin', `/banquet/events/${ev.id}?tab=lines`)
  await page.getByTestId('line-add').click()
  const d = page.getByTestId('line-dialog')
  await d.locator('select[name=what]').selectOption(`p${fullday.id}`)
  await shot(d, 'banquet-item-02')
  // STEP 3-4: Simpan, the estimate
  await page.getByTestId('line-save').click()
  await expect(page.getByTestId('line-dialog')).toBeHidden()
  await expect(page.getByTestId('estimate-total')).not.toHaveText('0')
  await shot(page.getByTestId('estimate-card'), 'banquet-item-04')
  // STEP 5-6: tab Deposit & kebijakan → Terima DP by bank transfer
  await page.getByTestId('tab-deposit').click()
  await page.getByTestId('receive-open').click()
  const r = page.getByTestId('receive-dialog')
  await r.locator('[name=receive_amount]').fill('5000000')
  await r.locator('select[name=receive_method]').selectOption('BANK_TRANSFER')
  await r.locator('[name=receive_reference]').fill('TRF-BCA-2411')
  await shot(r, 'banquet-item-07')
  // STEP 7: Terima
  await page.getByTestId('receive-save').click()
  await expect(page.getByTestId('receive-dialog')).toBeHidden()
  await expect(page.getByTestId('deposit-held')).toBeVisible()
}

export async function flowPostingSesi(page: Page, loginAs: LoginAs, data: Factories): Promise<void> {
  const ev = await bqEvent(data, BQ_EVENT)
  const sid = ev.sessions[0]!.id
  // STEP 1-3: tab Folio → Posting sesi. Pax aktual is left empty: typing in it throws in BanquetPostDialog.vue:57 (the number input
  // gives a number, the code calls trim), and the session is billed at the guaranteed pax anyway (_laporan.md 5.1)
  await loginAs('admin', `/banquet/events/${ev.id}?tab=folio`)
  await page.getByTestId(`folio-post-${sid}`).click()
  const d = page.getByTestId('post-dialog')
  await shot(d, 'banquet-posting-03')
  // STEP 4-5: Posting sesi, the folio card
  await page.getByTestId('post-save').click()
  await expect(page.getByTestId('toast-success').first()).toBeVisible()
  await expect(page.getByTestId(`folio-posted-${sid}`)).toBeVisible()
  await shot(page.getByTestId('folio-card'), 'banquet-posting-05')
}

export async function flowBeoDokumen(page: Page, loginAs: LoginAs, data: Factories): Promise<void> {
  const ev = await bqEvent(data, BQ_EVENT)
  const sid = ev.sessions[0]!.id
  // STEP 1-3: tab BEO, the kitchen text and the reason
  await loginAs('admin', `/banquet/events/${ev.id}?tab=beo`)
  const sess = page.getByTestId(`beo-session-${sid}`)
  await sess.getByTestId(`beo-text-${sid}-KITCHEN`).fill('Makan siang prasmanan 42 porsi, 3 vegetarian. Coffee break pukul 10.00 dan 15.00.')
  await sess.getByPlaceholder(tx('banquet.beo.notePlaceholder')).fill('Tambah menu vegetarian')
  await shot(sess.getByTestId(`beo-${sid}-KITCHEN`), 'banquet-beo-03')
  // STEP 4: Simpan sebagai revisi
  await sess.getByTestId(`beo-save-${sid}`).click()
  await expect(page.getByTestId('toast-success').first()).toBeVisible()
  // STEP 6: tab Dokumen
  await page.getByTestId('tab-docs').click()
  await expect(page.getByTestId('docs-list')).toBeVisible()
  await shot(page.getByTestId('docs-list'), 'banquet-beo-06')
}

export async function flowBatalAcara(page: Page, loginAs: LoginAs, w: DocsWorld, data: Factories): Promise<void> {
  // A Definit event 40 days ahead with a package and a deposit, made by the API
  const P = `${data.P}/banquet`
  const company = await data.company({ name: 'PT Bumi Lestari' })
  const rooms = await data.api.all<{ id: number; code: string }>(`${P}/rooms`)
  const pkgs = await data.api.all<{ id: number; code: string }>(`${P}/packages`)
  const tpls = await data.api.all<{ id: number; code: string }>(`${P}/policy-templates`)
  const ev = await data.api.post<{ id: number }>(`${P}/events`, { name: 'Seminar Keuangan Daerah', booker_company_id: company.id, contact_name: 'Bapak Yoga' })
  const withS = await data.api.post<{ sessions: { id: number }[] }>(`${P}/events/${ev.id}/sessions`, {
    name: 'Seminar', room_id: rooms.find((r) => r.code === 'SARB')!.id, session_date: addDays(w.businessDate, 40), start_time: '09:00', end_time: '16:00',
    pax_estimated: 80, pax_guaranteed: 70, accept_capacity: true,
  })
  await data.api.post(`${P}/events/${ev.id}/tentative`, { option_until: w.businessDate })
  await data.api.post(`${P}/events/${ev.id}/confirm`, { policy_template_id: tpls.find((t) => t.code === 'STANDARD')!.id })
  await data.api.post(`${P}/events/${ev.id}/lines`, { session_id: withS.sessions[0]!.id, package_id: pkgs.find((p) => p.code === 'FULLDAY')!.id, price_mode: 'EXCLUSIVE' })
  const folio = await data.api.post<{ id: number }>(`${P}/events/${ev.id}/folio`)
  await data.pay(folio.id, '10000000', 'BANK_TRANSFER')
  // STEP 1-4: Batalkan acara, the reason
  await loginAs('admin', `/banquet/events/${ev.id}`)
  await page.getByTestId('event-cancel').click()
  const d = page.getByTestId('close-dialog')
  await expect(d.getByTestId('close-tier')).toBeVisible()
  await selectContaining(d.locator('select[name=reason_id]'), 'Dibatalkan pemesan')
  await d.locator('[name=reason_note]').fill('Seminar dipindah ke kantor pusat')
  await shot(d, 'banquet-batal-04')
  // STEP 5: save, the result
  await d.getByTestId('close-save').click()
  await expect(page.getByTestId('close-result')).toBeVisible()
  await shot(page.getByTestId('close-result'), 'banquet-batal-05')
}

// --- 06-corporate-group-city-ledger. Only the administrator holds company.manage, group.manage, folio.master_* and cityledger.*; the
// supervisor approves. No seed makes companies or groups: "06 perusahaan" makes PT Sinar Wisata Bali on screen, the next tests use it.

const CL_CODE = 'SINAR'
const CL_GUEST = { first_name: 'Rudi', last_name: 'Hartanto', phone: '+62 812 3300 4411', email: 'rudi.hartanto@contoh.example' }

async function clCompany(data: Factories): Promise<{ id: number; code: string }> {
  const list = await data.api.all<{ id: number; code: string }>(`${data.P}/companies`)
  const c = list.find((x) => x.code === CL_CODE)
  if (!c) throw new Error(`company ${CL_CODE} not found`)
  return c
}

/** 06-corporate-group-city-ledger/perusahaan.md */
export async function flowPerusahaan(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1-2: Pengaturan → Perusahaan → Perusahaan baru
  await loginAs('admin', '/setup/companies')
  await page.getByTestId('new-company').click()
  const form = page.getByTestId('company-form')
  await form.locator('input[name=code]').fill(CL_CODE)
  await form.locator('input[name=name]').fill('PT Sinar Wisata Bali')
  await form.locator('input[name=contact_name]').fill('Ibu Ratna Dewi')
  await form.locator('input[name=email]').fill('keuangan@sinarwisata.example')
  await form.locator('input[name=phone]').fill('+62 361 470 123')
  await form.locator('input[name=city]').fill('Denpasar')
  await form.locator('input[name=payment_terms_days]').fill('30')
  await form.locator('input[name=unlimited]').uncheck()
  await form.locator('input[name=credit_limit]').fill('25000000')
  await shot(form, 'perusahaan-baru-02')
  // STEP 3: Simpan
  await form.getByRole('button', { name: tx('common.save'), exact: true }).click()
  await expect(page.getByTestId(`company-${CL_CODE}`)).toBeVisible()
  await shotAround([page.locator('main h1'), page.getByTestId(`company-${CL_CODE}`)], 'perusahaan-daftar-03')
}

/** 06-corporate-group-city-ledger/grup-dan-master-folio.md */
export async function flowGrup(page: Page, loginAs: LoginAs, w: DocsWorld): Promise<void> {
  // STEP 1-2: Front Desk → Grup → Grup baru
  await loginAs('admin', '/groups')
  await page.getByTestId('new-group').click()
  const g = page.getByTestId('group-form')
  await g.locator('input[name=code]').fill('RAKER26')
  await g.locator('input[name=name]').fill('Rapat Kerja Sinar Wisata')
  await pickCombo(g, 'company_id', CL_CODE)
  await setDate(g, 'arrival_date', w.businessDate)
  await setDate(g, 'departure_date', addDays(w.businessDate, 2))
  await g.locator('input[name=contact_name]').fill('Ibu Ratna Dewi')
  await shot(g, 'grup-baru-02')
  // STEP 3: Simpan, open the group
  await g.getByRole('button', { name: tx('common.save'), exact: true }).click()
  await page.getByTestId('group-RAKER26').getByRole('link').first().click()
  // STEP 5-6: Buka master folio, the room charges of every member go to it
  const m = page.getByTestId('group-master')
  await m.getByTestId('master-open').click()
  await expect(m.getByTestId('master-notice')).toBeVisible()
  await m.locator('input[name=route_room]').check()
  await m.getByTestId('routing-save').click()
  await expect(m.getByTestId('master-summary')).toBeVisible()
  await shot(m, 'grup-master-06')
}

/** 06-corporate-group-city-ledger/transfer-ke-piutang-perusahaan.md. The stay then checks out by the API, for the invoice pictures. */
export async function flowTransferPiutang(page: Page, loginAs: LoginAs, data: Factories): Promise<void> {
  const company = await clCompany(data)
  const guest = await data.guest(CL_GUEST)
  const res = await data.reservation({ nights: 1, guest, companyId: company.id })
  const full = await data.api.get<{ version: number; rooms: { id: number }[] }>(`${data.P}/reservations/${res.id}`)
  const ci = await data.api.post<{ stay: { id: number }; folio: { id: number } }>(`${data.P}/reservations/${res.id}/rooms/${full.rooms[0]!.id}/check-in`,
    { version: full.version, guest_id: guest.id, adult_count: 2, room_id: res.room!.id })
  await data.postCharge(ci.folio.id, 'FOOD_BEVERAGE', '450000', 'Makan malam tamu perusahaan')
  const balance = (await data.folio(ci.folio.id)).balance
  // STEP 1-3: the folio → tab Transfer, the whole balance to PT Sinar Wisata Bali
  await loginAs('admin', `/folios/${ci.folio.id}?tab=transfer`)
  const form = page.getByTestId('transfer-form')
  await pickCombo(form, 'transfer_company', CL_CODE)
  await form.locator('input[name=transfer_amount]').fill(String(Math.round(Number(balance))))
  await form.locator('input[name=transfer_reference]').fill('PO-SWB-0457')
  await shot(form, 'piutang-transfer-03')
  // STEP 4: Transfer
  await form.getByRole('button', { name: tx('folio.transferButton') }).click()
  await expect(page.getByTestId('notice')).toHaveText(tx('folio.noticeTransfer'))
  // Check-out by the API: tonight's room charge is paid by card first, the check-out posts it
  const bd = await data.businessDate()
  const preview = await data.api.post<{ items: { folio_id: number | null; status: string; total: string }[] }>(`${data.P}/night-audit/room-charges/preview`, { business_date: bd, stay_ids: [ci.stay.id] })
  const tonight = preview.items.filter((i) => i.status === 'READY' && i.folio_id === ci.folio.id).reduce((n, i) => n + Number(i.total), 0)
  if (tonight > 0) await data.pay(ci.folio.id, String(tonight), 'CARD')
  const st = await data.stay(ci.stay.id)
  await data.api.post(`${data.P}/stays/${ci.stay.id}/check-out`, { version: st.stay.version, confirm_early_departure: true })
}

/** 06-corporate-group-city-ledger/invoice-dan-penerimaan.md */
export async function flowInvoice(page: Page, loginAs: LoginAs, data: Factories): Promise<void> {
  const company = await clCompany(data)
  // STEP 1-4: Tagihan → Piutang perusahaan → the company, Invoice baru
  await loginAs('admin', `/city-ledger/${company.id}`)
  const b = page.getByTestId('invoice-builder')
  await b.getByTestId('pick-all').check()
  await b.locator('input[name=invoice_notes]').fill('PO-SWB-0457, rapat kerja November')
  await shot(b, 'piutang-invoice-03')
  // STEP 5: Buat invoice
  await b.getByTestId('create-invoice').click()
  await expect(page.getByTestId('notice')).toContainText(/CINV/)
  const inv = ((await page.getByTestId('notice').innerText()).match(/CINV\d+/) ?? [''])[0]
  // STEP 7-8: Bayar on the invoice, part of it
  await page.getByTestId(`pay-${inv}`).click()
  const pf = page.getByTestId('pay-form')
  await pf.locator('input[name=pay_amount]').fill('300000')
  await pf.locator('select[name=pay_method]').selectOption('BANK_TRANSFER')
  await pf.locator('input[name=pay_reference]').fill('TRF-SWB-1124')
  await shot(pf, 'piutang-invoice-07')
  await pf.getByTestId('pay-submit').click()
  await expect(page.getByTestId('notice')).toContainText(inv)
  await shot(page.getByTestId('invoices'), 'piutang-invoice-09')
}

/** 06-corporate-group-city-ledger/nota-kredit-dan-penghapusan-piutang.md */
export async function flowNotaKredit(page: Page, loginAs: LoginAs, data: Factories, seed: SeedState): Promise<void> {
  const company = await clCompany(data)
  // STEP 1-3: the invoice → Nota kredit…
  await loginAs('admin', `/city-ledger/${company.id}`)
  const row = page.getByTestId('invoices').locator('[data-testid^="invoice-CINV"]').first()
  const inv = ((await row.getAttribute('data-testid')) ?? '').replace('invoice-', '')
  await page.getByTestId(`credit-${inv}`).click()
  const f = page.getByTestId('adjust-form')
  await f.locator('input[name=line_description_0]').fill('Potongan makan malam: hidangan terlambat')
  await pickCombo(f, 'line_account_0', '4290')
  await selectContaining(f.locator('select[name=line_department_0]'), 'FB')
  await f.locator('input[name=line_net_0]').fill('50000')
  await f.locator('input[name=adjust_reason]').fill('Keluhan tamu, disetujui manajer F&B')
  await shot(f, 'piutang-nota-03')
  // STEP 4: Lanjutkan → approval by the supervisor
  await f.getByTestId('adjust-continue').click()
  await approve(page, seed.users.supervisor.email, seed.users.supervisor.password)
  await expect(page.getByTestId('notice')).toHaveText(tx('clAccount.creditNoteMade'))
  await shot(page.getByTestId('adjustments'), 'piutang-nota-05')
}

/** 06-corporate-group-city-ledger/surat-peringatan.md: no invoice is overdue in the pictures' hotel (that needs a night audit after
 *  the due date), so only the page and its late fee. */
export async function flowSuratPeringatan(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1-2: Tagihan → Invoice jatuh tempo, the late fee
  await loginAs('admin', '/city-ledger/overdue')
  await expect(page.getByTestId('overdue-summary')).toBeVisible()
  const lf = page.getByTestId('late-fee')
  await lf.locator('input[name=monthly_rate]').fill('2')
  await lf.locator('input[name=grace_days]').fill('7')
  await shotAround([page.locator('main h1'), lf], 'piutang-peringatan-02')
}

// --- 08-keuangan-back-office. The accounting user (payables, bank, budget) and the administrator (tax: nobody else holds tax.manage,
// tax.file, tax.invoice; budget.approve). Nothing of this area is seeded but the suppliers SUMBER and DEMO-WL-SUP.

/** 08-keuangan-back-office/utang-usaha.md */
export async function flowUtangUsaha(page: Page, loginAs: LoginAs, w: DocsWorld, data: Factories): Promise<void> {
  await data.api.post(`${data.P}/payables/suppliers`, { code: 'BERSIH', name: 'CV Bersih Cemerlang', contact_name: 'Pak Made Sudana', city: 'Gianyar', payment_terms_days: 30 })
  // STEP 1-3: Utang usaha → Tagihan pemasok → Catat tagihan
  await loginAs('accounting', '/payables/bills')
  await page.getByTestId('new-bill').click()
  const form = page.getByTestId('bill-form')
  await pickCombo(form, 'supplier_id', 'BERSIH')
  await form.locator('input[name=invoice]').fill('INV/BC/1124/031')
  await setDate(form, 'bill_date', w.businessDate)
  await form.locator('input[name=description]').fill('Bahan pembersih November')
  await pickCombo(form.getByTestId('line-0'), 'account_0', '5140 ·')
  await selectContaining(form.locator('select[name=department_0]'), 'ROOMS')
  await form.locator('input[name=detail_0]').fill('Cairan pembersih lantai 20 jeriken')
  await form.locator('input[name=amount_0]').fill('2400000')
  await shot(form, 'utang-tagihan-03')
  // STEP 4: Catat tagihan
  await form.getByTestId('bill-post').click()
  await expect(page.getByTestId('notice')).toBeVisible()
  // STEP 5-7: Pembayaran pemasok → Bayar pemasok
  await page.goto('/payables/payments')
  await page.getByTestId('new-payment').click()
  const pay = page.getByTestId('payment-form')
  await pickCombo(pay, 'supplier_id', 'BERSIH')
  await setDate(pay, 'payment_date', w.businessDate)
  await pay.locator('input[name=reference]').fill('TRF-BCA-88120')
  await expect(pay.getByTestId('open-bills')).toBeVisible()
  await pay.getByTestId('pay-all').click()
  await shot(pay, 'utang-bayar-06')
  await pay.getByTestId('payment-post').click()
  await expect(page.getByTestId('notice')).toBeVisible()
  // STEP 9: Umur utang usaha
  await page.goto('/payables/aging')
  await expect(page.getByTestId('aging')).toBeVisible()
  await shotAround([page.locator('main h1'), page.getByTestId('aging')], 'utang-umur-09')
}

/** 08-keuangan-back-office/pajak-dan-faktur.md */
export async function flowPajak(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1: Pajak → Status PKP
  await loginAs('admin', '/tax/status')
  await expect(page.getByTestId('current')).toBeVisible()
  await shotAround([page.locator('main h1'), page.getByTestId('history')], 'pajak-status-01')
  // STEP 3-4: Pajak → Profil pelaporan pajak → Atur pajak, PB1
  await page.goto('/tax/profiles')
  await page.getByTestId('new-profile').click()
  const form = page.getByTestId('profile-form')
  await pickCombo(form, 'tax_id', /^PB1 ·/)
  await form.locator('input[name=authority]').fill('Bapenda Kabupaten Gianyar')
  await form.locator('input[name=registration_number]').fill('973/0412/BPD-GNR')
  await form.locator('input[name=due_day]').fill('15')
  await shot(form, 'pajak-profil-04')
  await form.getByRole('button', { name: tx('common.save'), exact: true }).click()
  await expect(page.getByTestId('profile-PB1')).toBeVisible()
  // STEP 6: Pajak → SPT Masa PPN, the months of PB1
  await page.goto('/tax/returns')
  await selectContaining(page.locator('select[name=tax]'), 'PB1')
  await expect(page.getByTestId('periods')).toBeVisible()
  await shotAround([page.locator('main h1'), page.getByTestId('periods')], 'pajak-spt-06')
}

/** 08-keuangan-back-office/rekonsiliasi-bank.md */
export async function flowRekonsiliasiBank(page: Page, loginAs: LoginAs, w: DocsWorld): Promise<void> {
  // STEP 1-2: Bank → Rekening bank → Daftarkan rekening
  await loginAs('accounting', '/bank/accounts')
  await page.getByTestId('new-bank').click()
  const form = page.getByTestId('bank-form')
  await pickCombo(form, 'account_id', '1130 ·')
  await form.locator('input[name=name]').fill('BCA operasional')
  await form.locator('input[name=account_number]').fill('146 000 7788')
  await shot(form, 'bank-rekening-02')
  await form.getByRole('button', { name: tx('common.save'), exact: true }).click()
  await expect(page.getByTestId('bank-1130')).toBeVisible()
  // STEP 3-5: Bank → Mutasi bank → Impor rekening koran
  await page.goto('/bank/statements')
  await expect(page.getByTestId('new-statement')).toBeVisible()
  await page.getByTestId('new-statement').click()
  const imp = page.getByTestId('import-form')
  await setDate(imp, 'period_from', addDays(w.businessDate, -6))
  await setDate(imp, 'period_to', w.businessDate)
  await imp.locator('input[name=opening_balance]').fill('50000000')
  await imp.locator('input[name=closing_balance]').fill('49250000')
  const d1 = addDays(w.businessDate, -2)
  const d2 = addDays(w.businessDate, -1)
  await imp.locator('textarea[name=csv]').fill(`date,description,reference,amount\n${d1},Setoran tunai,STR-0098,1500000\n${d2},Biaya administrasi,ADM-11,-250000\n${d2},Transfer keluar,TRF-7781,-2000000`)
  await shot(imp, 'bank-impor-04')
  await imp.getByTestId('import-run').click()
  await expect(page.getByTestId('notice')).toBeVisible()
  // STEP 6-7: Rekonsiliasi, Cocokkan otomatis
  await page.getByTestId('statements').locator('[data-testid^="open-"]').first().click()
  await expect(page.getByTestId('summary')).toBeVisible()
  await page.getByTestId('auto-match').click()
  await expect(page.getByTestId('notice')).toBeVisible()
  await shotAround([page.locator('main h1'), page.getByTestId('lines')], 'bank-rekonsiliasi-07')
}

/** 08-keuangan-back-office/settlement-kartu.md */
export async function flowSettlementKartu(page: Page, loginAs: LoginAs, w: DocsWorld): Promise<void> {
  // STEP 1: Bank → Settlement kartu, what the acquirer still owes
  await loginAs('accounting', '/bank/cards')
  await expect(page.getByTestId('expected')).toBeVisible()
  await shotAround([page.locator('main h1'), page.getByTestId('expected')], 'kartu-belum-01')
  // STEP 2-3: Aturan fee, a card rule
  const r = page.getByTestId('rule-form')
  await r.locator('select[name=rule_method]').selectOption('CARD')
  await r.locator('input[name=mdr_rate]').fill('2.5')
  await r.locator('input[name=vat_rate]').fill('11')
  await r.locator('input[name=settlement_days]').fill('2')
  await setDate(r, 'effective_from', w.businessDate)
  await r.getByTestId('add-rule').click()
  await expect(page.getByTestId('rule-list')).toBeVisible()
  await shot(page.getByTestId('rules'), 'kartu-aturan-03')
}

/** 08-keuangan-back-office/anggaran.md */
export async function flowAnggaran(page: Page, loginAs: LoginAs, seed: SeedState): Promise<void> {
  // STEP 1-2: Anggaran → Anggaran baru
  await loginAs('accounting', '/budget')
  await page.getByTestId('new-budget').click()
  const form = page.getByTestId('new-form')
  await selectContaining(form.locator('select[name=year_start]'), '2026-01-01')
  await form.locator('input[name=name]').fill('Anggaran 2026')
  await form.locator('input[name=description]').fill('Rencana pendapatan kamar dan biaya utama')
  await shot(form, 'anggaran-baru-02')
  await form.getByTestId('create').click()
  await expect(page).toHaveURL(/\/budget\/\d+/)
  // STEP 4-5: add the room revenue account, spread a year over the months
  await selectContaining(page.locator('select[name=add_account]'), '4110')
  await page.getByTestId('add-row').click()
  await page.locator('[data-testid^="spread-"]').first().click()
  const sp = page.getByTestId('spread-form')
  await sp.locator('input[name=spread_total]').fill('1800000000')
  await sp.locator('select[name=spread_method]').selectOption('EQUAL')
  await sp.getByTestId('spread-apply').click()
  await expect(page.getByTestId('notice')).toBeVisible()
  // The table is wider than its card: shoot the scroll box from its left edge
  // (a shot of the table itself scrolls it sideways under the sidebar).
  const gridBox = page.getByTestId('grid').locator('..')
  await gridBox.evaluate((el) => { el.scrollLeft = 0 })
  await shot(gridBox, 'anggaran-angka-05')
  // STEP 7: Aktifkan, approved by the administrator
  await page.getByTestId('activate').click()
  await approve(page, seed.users.admin.email, seed.users.admin.password)
  await expect(page.getByTestId('status')).toHaveAttribute('data-status', 'ACTIVE')
  // STEP 8: Anggaran terhadap realisasi
  await page.goto('/budget/vs-actual')
  await expect(page.getByTestId('report')).toBeVisible()
  await shotAround([page.locator('main h1'), page.getByTestId('report')], 'anggaran-realisasi-08')
}
