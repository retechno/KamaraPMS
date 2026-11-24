import { expect, type Locator, type Page } from 'playwright/test'
import { addDays } from '../support/factories'
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
