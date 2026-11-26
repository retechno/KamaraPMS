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
  await expect(wizard.getByTestId('step-departure')).toHaveAttribute('data-settled', 'true') // the server's preview has said whether it is early
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

/** 04-pos/mengembalikan-beban-kamar.md (KD-01, KD-02) */
export async function flowKembalikanBebanKamar(page: Page, loginAs: LoginAs, w: DocsWorld, seed: SeedState): Promise<void> {
  const made = w.inHouse.made
  const admin = seed.users.admin
  // (a coffee charged by mistake to the room of Made Wirawan, whose folio is open for the restaurant; the duty manager holds the reopening, the return and the approval)
  await loginAs('admin', '/pos/floor')
  await chooseOutlet(page.getByTestId('outlet-select'), RESTO)
  await page.getByTestId('new-takeaway').click()
  const take = page.getByTestId('takeaway-dialog')
  await take.locator('input[name=customer_name]').fill('Tamu restoran')
  await take.getByTestId('open-takeaway').click()
  await expect(page).toHaveURL(/\/pos\/bills\/\d+$/)
  await addProduct(page, 'Minuman', /Kopi Bali/)
  await page.getByTestId('send').click()
  await expect(page.getByTestId('round-1')).toBeVisible()
  await page.getByTestId('pay').click()
  const pay = page.getByTestId('pay-dialog')
  await onlyMethod(pay, 'ROOM')
  await pay.getByTestId('room-query').fill(made.room)
  await pay.getByTestId('room-find').click()
  await pay.getByTestId(`room-${made.stayId}`).click()
  await pay.getByTestId('exact-ROOM').click()
  await pay.getByTestId('pay-submit').click()
  await expect(page.getByTestId('bill-closed')).toBeVisible()
  // STEP 1: Buka kembali, the reason and the approval
  await page.getByTestId('reopen').click()
  await page.getByTestId('reason-dialog').getByTestId('reason').fill('Salah kamar: tamu restoran, bukan tamu kamar')
  await page.getByTestId('reason-dialog').getByTestId('reason-submit').click()
  await approve(page, admin.email, admin.password)
  await expect(page.getByTestId('actions')).toBeVisible()
  // STEP 2: the charge to the room with Kembalikan beban kamar
  const charges = page.getByTestId('room-charges')
  await expect(charges.locator('[data-testid^=return-charge-]')).toBeVisible()
  await shot(page.getByTestId('bill-panel'), 'pos-kembalikan-02')
  // STEP 3: the reason → Lanjut ke persetujuan
  await charges.locator('[data-testid^=return-charge-]').click()
  const sheet = page.getByTestId('reason-dialog')
  await sheet.getByTestId('reason').fill('Salah kamar: tamu restoran, bukan tamu kamar')
  await shot(sheet, 'pos-kembalikan-03')
  await sheet.getByTestId('reason-submit').click()
  // STEP 4: the approval; the charge is gone and the bill is to pay again
  await approve(page, admin.email, admin.password)
  await expect(page.getByTestId('room-charges')).toHaveCount(0)
  await shot(page.getByTestId('bill-panel'), 'pos-kembalikan-04')
  // STEP 5: paid another way (by card)
  await page.getByTestId('pay').click()
  await onlyMethod(pay, 'CARD')
  await pay.getByTestId('exact-CARD').click()
  await pay.getByTestId('pay-submit').click()
  await expect(page.getByTestId('bill-closed')).toBeVisible()
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
  // STEP 1-3: tab Folio → Posting sesi, 42 came for 40 guaranteed: Ditagih follows the number typed (KD-03)
  await loginAs('admin', `/banquet/events/${ev.id}?tab=folio`)
  await page.getByTestId(`folio-post-${sid}`).click()
  const d = page.getByTestId('post-dialog')
  await d.locator('input[name=pax_actual]').fill('42')
  await expect(d.getByTestId('post-billed')).toHaveText('42')
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

// --- 10-aset-tetap (the tenant admin: no E2E role holds asset.*; pms-seed assets ran in the E2E setup)

async function openAsset(page: Page, name: string): Promise<void> {
  await page.goto('/assets')
  await page.getByTestId('filter-q').fill(name)
  await page.getByTestId('filter-q').press('Enter')
  await page.getByTestId('asset-table').getByRole('link', { name }).first().click()
  await expect(page.locator('main h1')).toContainText(name)
}

export async function flowDaftarTunggu(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1: Aset tetap → Daftar tunggu
  await loginAs('admin', '/assets/waiting')
  const table = page.getByTestId('waiting-table')
  await expect(table).toBeVisible()
  await shot(table, 'aset-tunggu-01')
  // STEP 2-4: Jadikan aset on the restaurant chairs, twelve units
  const row = table.locator('[data-testid^="line-"]').filter({ hasText: 'Kursi restoran' }).first()
  await row.locator('[data-testid^="make-"]').click()
  const dlg = page.getByTestId('make-dialog')
  await dlg.locator('input[name=name]').fill('Kursi restoran')
  await dlg.locator('input[name=units]').fill('12')
  await expect(dlg.getByTestId('make-units')).toBeVisible()
  await shot(dlg, 'aset-tunggu-04')
  await dlg.getByTestId('make-confirm').click()
  await expect(page.getByTestId('toast-success').first()).toBeVisible()
  // STEP 5-6: the register
  await page.goto('/assets')
  await expect(page.getByTestId('asset-table')).toBeVisible()
  await shotPage(page, 'aset-register-06')
}

export async function flowSaldoAwalAset(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1-2: Aset tetap → Saldo awal aset, one old asset
  await loginAs('admin', '/assets/opening')
  await page.getByTestId('toggle-single').click()
  const form = page.getByTestId('opening-form')
  await setDate(form, 'as_of', '2026-10-31')
  const single = page.getByTestId('single-form')
  await single.locator('input[name=name]').fill('Meja resepsionis')
  await single.locator('select[name=category]').selectOption('FFE')
  await single.locator('input[name=cost]').fill('15000000')
  await setDate(single, 'acquisition_date', '2024-01-02')
  await setDate(single, 'in_service_date', '2024-01-02')
  await single.locator('input[name=accumulated_depreciation]').fill('5312500')
  await single.locator('input[name=months_done]').fill('34')
  await shotAround([page.locator('main h1'), page.getByTestId('opening-form')], 'aset-saldoawal-03')
  // STEP 4-5: Periksa (uji coba), then Impor
  await page.getByTestId('dry-run').click()
  const result = page.getByTestId('opening-result')
  await expect(page.getByTestId('confirm-import')).toBeEnabled()
  await shot(result, 'aset-saldoawal-05')
  await page.getByTestId('confirm-import').click()
  await expect(page).toHaveURL(/\/assets$/)
}

export async function flowPindahAset(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1: the asset page
  await loginAs('admin')
  await openAsset(page, 'Sofa lobi')
  await shotPage(page, 'aset-detail-01')
  // STEP 2-3: Pindahkan to the front office
  await page.getByTestId('act-move').click()
  const dlg = page.getByTestId('move-dialog')
  await dlg.getByTestId('move-location-on').check()
  await selectContaining(dlg.getByTestId('move-location'), 'KANTOR')
  await dlg.locator('input[name=reason], textarea[name=reason]').fill('Lobi direnovasi, sofa sementara di kantor depan')
  await shot(dlg, 'aset-pindah-03')
  await dlg.getByTestId('move-confirm').click()
  await expect(page.getByTestId('toast-success').first()).toBeVisible()
  // STEP 5-6: Ubah umur / nilai sisa / metode (shown, not saved)
  await page.getByTestId('act-terms').click()
  const rev = page.getByTestId('revise-dialog')
  await rev.getByTestId('revise-kind').selectOption('LIFE')
  await rev.getByTestId('revise-life').fill('120')
  await rev.locator('input[name=reason], textarea[name=reason]').fill('Masih layak pakai lebih lama dari perkiraan')
  await shot(rev, 'aset-ubah-06')
  await page.keyboard.press('Escape')
}

export async function flowPenyusutan(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1-2: Aset tetap → Penyusutan, the preview of November (not ended yet in the E2E world)
  await loginAs('admin', '/assets/depreciation')
  await page.getByTestId('month-2026-11').click()
  const preview = page.getByTestId('depr-preview')
  await expect(preview).toBeVisible()
  await expect(page.getByTestId('depr-journal')).toBeVisible()
  await shot(preview, 'aset-penyusutan-02')
}

export async function flowPelepasanAset(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1-3: Lepas / jual: the car is sold
  await loginAs('admin')
  await openAsset(page, 'Toyota Innova')
  await page.getByTestId('act-dispose').click()
  const dlg = page.getByTestId('dispose-dialog')
  await dlg.getByTestId('dispose-sale').check()
  await dlg.getByTestId('dispose-price').fill('300000000')
  await selectContaining(dlg.getByTestId('dispose-account'), '1130')
  await expect(dlg.getByTestId('dispose-journal')).toBeVisible()
  await tallWindow(page, 1600, () => shot(dlg, 'aset-lepas-03', { pad: 0 }))
  await dlg.getByTestId('dispose-confirm').click()
  await expect(page.getByTestId('toast-success').first()).toBeVisible()
  // STEP 5-6: Pelepasan aset: the request of the asset clerk waits for a decision
  await page.goto('/assets/disposals')
  const pending = page.getByTestId('pending-list')
  await expect(pending).toBeVisible()
  await shotAround([page.getByTestId('tab-pending'), pending], 'aset-lepas-06')
  const approveBtn = pending.locator('[data-testid^="approve-"]').first()
  if (await approveBtn.count()) {
    await approveBtn.click()
    await expect(page.getByTestId('toast-success').first()).toBeVisible()
  }
  // STEP 8: the posted disposals
  await page.getByTestId('tab-disposals').click()
  const list = page.getByTestId('disposal-list')
  await expect(list).toBeVisible()
  await shotAround([page.getByTestId('tab-disposals'), list], 'aset-lepas-08')
}

export async function flowOpnameAset(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1-2: Label QR
  await loginAs('admin', '/assets/labels')
  await expect(page.getByTestId('labels-print')).toBeVisible()
  await shotAround([page.locator('main h1'), page.getByTestId('labels-print')], 'aset-label-02')
  // STEP 4: Opname aset: the list
  await page.goto('/assets/audits')
  const audits = page.getByTestId('asset-audits')
  await expect(audits.locator('[data-testid^="audit-"]').first()).toBeVisible()
  await shotPage(page, 'aset-opname-04')
  // STEP 5-6: the open stock-take of the demo data
  await audits.locator('li[data-testid^="audit-"]').first().getByRole('link').click()
  await expect(page.getByTestId('asset-audit')).toBeVisible()
  await expect(page.getByTestId('audit-counts')).toBeVisible()
  await shotPage(page, 'aset-opname-06')
}

export async function flowLaporanAset(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1: Aset tetap → Laporan aset
  await loginAs('admin', '/assets/reports')
  await expect(page.getByTestId('report-register')).toBeVisible()
  await shotPage(page, 'aset-laporan-01')
  // STEP 2-3: the register on a date
  await page.getByTestId('report-register').click()
  await page.getByTestId('report-run').click()
  const table = page.getByTestId('register-table')
  await expect(table).toBeVisible()
  await shotAround([table], 'aset-laporan-03')
}

// --- 09-inventory. The tenant admin drives the screens (no E2E role holds inventory.manage, request, purchase, receive, move or count). Where a
// document needs a second person (nobody approves a document of their own, the administrator included), the approvers of the seed decide:
// invHead (head of every department, level 1), invCost (level 2 of a purchase request), invFinal (last level of a store requisition) and
// invApprover (a count). pms-seed inventory ran without its purchasing demo (one administrator) and without the open count, so the supplier,
// the requests and the order are made here, in the order of the tests: "09 penerimaan" receives the order of "09 permintaan pembelian".
// A count opened here is approved, or cancelled, before its flow ends: an open count holds the night audit.

type ApiAs = (user: UserKey) => Promise<Factories['api']>

const INV_SUPPLIER = { code: 'SUMBER', name: 'CV Sumber Segar' }
const INV_ITEM = { code: 'KAPORIT', name: 'Kaporit kolam renang' }
const INV_CATEGORY = { code: 'KIMIA', name: 'Bahan kimia kolam' }

interface InvRow { id: number; code: string; name: string }

/** The rows of an inventory list ({ data } or, for the items, { items }). */
async function invRows<T>(data: Factories, path: string, query: Record<string, string | number | boolean> = {}): Promise<T[]> {
  const r = await data.api.get<{ data?: T[]; items?: T[] }>(`${data.P}${path}`, { limit: 200, ...query })
  return r.data ?? r.items ?? []
}

async function invByCode(data: Factories, path: string, code: string): Promise<InvRow> {
  const rows = await invRows<InvRow>(data, path, path === '/inventory/items' ? { q: code } : {})
  const row = rows.find((r) => r.code === code)
  if (!row) throw new Error(`${path}: ${code} not found`)
  return row
}

/** The unit of an item by its code (karton, kg): the units of an item come with its detail. */
async function invUnit(data: Factories, itemId: number, unitCode: string): Promise<number> {
  const it = await data.api.get<{ units: { unit_id: number; unit_code: string }[] }>(`${data.P}/inventory/items/${itemId}`)
  const u = it.units.find((x) => x.unit_code === unitCode)
  if (!u) throw new Error(`item ${itemId} has no unit ${unitCode}`)
  return u.unit_id
}

/** The supplier of the purchasing pages: pms-seed makes it only with its purchasing demo, which needs three administrators. */
async function invSupplier(data: Factories): Promise<number> {
  const find = async () => (await invRows<InvRow>(data, '/inventory/suppliers')).find((s) => s.code === INV_SUPPLIER.code)
  const found = await find()
  if (found) return found.id
  await data.api.post(`${data.P}/payables/suppliers`, { code: INV_SUPPLIER.code, name: INV_SUPPLIER.name, contact_name: 'Bu Ratna', city: 'Denpasar', payment_terms_days: 30 })
  const made = await find()
  if (!made) throw new Error('the supplier SUMBER was not made')
  return made.id
}

/** Lets the item details a line asked for come back (a line sets its unit, price and supplier when its item answers). */
async function invSettle(page: Page): Promise<void> {
  await page.waitForLoadState('networkidle', { timeout: 3_000 }).catch(() => { /* the shell polls: never idle for long */ })
}

/** Toasts left by an earlier step (they would cover the picture). */
async function closeToasts(page: Page): Promise<void> {
  const close = page.getByTestId('toast-close')
  for (let i = await close.count(); i > 0; i--) await close.first().click().catch(() => undefined)
  await expect(page.getByTestId('toast-success')).toHaveCount(0, { timeout: 10_000 }).catch(() => undefined)
}

/** A picture of something taller than the window: the window grows for the picture, then goes back to 1440×900. */
async function tallWindow(page: Page, height: number, take: () => Promise<void>, width?: number): Promise<void> {
  const vp = page.viewportSize() ?? { width: 1440, height: 900 }
  await page.setViewportSize({ width: width ?? vp.width, height })
  try {
    await take()
  } finally {
    await page.setViewportSize(vp)
  }
}

/** Fills line i of a document: the item (by its code), then its unit (by its exact label) once the item has answered. */
async function invLine(page: Page, form: Locator, i: number, item: string, unit: string | null, qty: string): Promise<void> {
  if (!(await form.locator(`select[name=item_${i}]`).count())) await form.getByTestId('line-add').click()
  await expect(form.locator(`select[name=item_${i}]`)).toBeVisible()
  await invSettle(page)
  await selectContaining(form.locator(`select[name=item_${i}]`), `${item} ·`)
  await invSettle(page)
  if (unit) {
    const u = form.locator(`select[name=unit_${i}]`)
    await expect(u.locator('option', { hasText: unit }).first()).toBeAttached()
    await selectContaining(u, unit) // the label carries more than the unit name (e.g. "karung (25 kg)")
  }
  await form.locator(`input[name=qty_${i}]`).fill(qty)
}

/** 09-inventory/menyiapkan-persediaan.md */
export async function flowMenyiapkanPersediaan(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1-2: Persediaan → Harga pokok: the method (locked by the opening stock of the seed) and the setup check
  await loginAs('admin', '/inventory/costing')
  await expect(page.getByTestId('method-AVERAGE')).toBeVisible()
  await expect(page.getByTestId('setup')).toBeVisible()
  await shotAround([page.getByTestId('method'), page.getByTestId('setup')], 'inventaris-siapkan-02')
  // STEP 4-5: Satuan → Satuan baru: the jerrycan
  await page.goto('/inventory/units')
  await page.getByTestId('unit-new').click()
  const unit = page.getByTestId('unit-form')
  await unit.locator('input[name=code]').fill('jrg')
  await unit.locator('input[name=name]').fill('Jeriken')
  await shot(unit, 'inventaris-siapkan-05')
  await page.getByTestId('unit-save').click()
  await expect(page.getByTestId('notice')).toBeVisible()
  // STEP 6-8: Kategori & akun → Kategori baru: the pool chemicals (the item of the next page goes into it)
  await page.goto('/inventory/categories')
  await page.getByTestId('category-new').click()
  const cat = page.getByTestId('category-form')
  await cat.locator('input[name=code]').fill(INV_CATEGORY.code)
  await cat.locator('input[name=name]').fill(INV_CATEGORY.name)
  await cat.locator('select[name=kind]').selectOption('SUPPLIES')
  await selectContaining(cat.locator('select[name=inventory_account_id]'), '1330 ·')
  await shot(cat, 'inventaris-siapkan-07')
  await page.getByTestId('category-save').click()
  await expect(page.getByTestId('notice')).toBeVisible()
  // STEP 9-10: back on Harga pokok, the setup check and the queue of the POS sales
  await page.goto('/inventory/costing')
  await expect(page.getByTestId('queue')).toBeVisible()
}

/** 09-inventory/barang-gudang-dan-saldo-awal.md */
export async function flowBarangGudang(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1-3: Barang & stok → Barang baru, kept in kg and bought in sacks of 25 kg
  await loginAs('admin', '/inventory/items')
  await page.getByTestId('item-new').click()
  const form = page.getByTestId('item-form')
  await form.locator('input[name=code]').fill(INV_ITEM.code)
  await form.locator('input[name=name]').fill(INV_ITEM.name)
  await selectContaining(form.locator('select[name=category_id]'), `${INV_CATEGORY.name} (`)
  await selectContaining(form.locator('select[name=stock_unit_id]'), 'kg ·')
  await form.locator('input[name=reference_cost]').fill('32000')
  await form.getByTestId('unit-add').click()
  const row = form.getByTestId('unit-row-0')
  await selectContaining(row.locator('select[name=unit_0]'), 'karung ·')
  await row.locator('input[name=num_0]').fill('25')
  await row.locator('input[name=den_0]').fill('1')
  // the dot does not stay drawn after the click (the choice itself is kept: KD in barang-gudang-dan-saldo-awal.md)
  await row.getByRole('radio', { name: tx('inventory.itemForm.defaultPurchase') }).click()
  await shot(form, 'inventaris-barang-03')
  // STEP 4: Simpan
  await page.getByTestId('item-save').click()
  await expect(page).toHaveURL(/\/inventory\/items\/\d+$/)
  // STEP 5-7: Gudang → UTAMA, its minimum level for the new item
  await page.goto('/inventory/warehouses')
  await page.getByTestId('warehouse-UTAMA').getByRole('link', { name: 'UTAMA' }).click()
  const levels = page.getByTestId('levels')
  await expect(levels).toBeVisible()
  await invSettle(page)
  await levels.getByTestId('level-add').click()
  const i = (await levels.locator('tr[data-testid^="level-"]').count()) - 1
  await selectContaining(levels.locator(`select[name=level_item_${i}]`), `${INV_ITEM.code} ·`)
  await levels.locator(`input[name=min_${i}]`).fill('50')
  await levels.locator(`input[name=max_${i}]`).fill('150')
  await levels.locator(`input[name=reorder_${i}]`).fill('100')
  await levels.locator(`tr[data-testid="level-${i}"]`).scrollIntoViewIfNeeded()
  await shot(levels, 'inventaris-barang-07')
  await levels.getByTestId('levels-save').click()
  await expect(page.getByTestId('notice')).toBeVisible()
  // STEP 8-9: Saldo awal → Saldo awal baru, 40 kg in the main store
  await page.goto('/inventory/openings/new')
  const op = page.getByTestId('opening-form')
  await selectContaining(op.locator('select[name=warehouse_id]'), 'UTAMA ·')
  await invLine(page, op, 0, INV_ITEM.code, 'kg', '40')
  await op.locator('input[name=cost_0]').fill('32000')
  await expect(op.getByTestId('line-value-0')).not.toBeEmpty()
  await shot(op, 'inventaris-barang-09')
  // STEP 10: Posting saldo awal
  await page.getByTestId('opening-post').click()
  await expect(page).toHaveURL(/\/inventory\/openings\/\d+$/)
  await expect(page.getByTestId('opening-posted')).toBeVisible()
  // STEP 11: the item on Barang & stok, its side panel
  await page.goto('/inventory/items')
  await page.getByTestId('item-search').fill(INV_ITEM.code)
  await page.getByTestId(`item-${INV_ITEM.code}`).click()
  await expect(page.getByTestId('item-panel')).toBeVisible()
}

/** 09-inventory/permintaan-dan-pesanan-pembelian.md */
export async function flowPermintaanPembelian(page: Page, loginAs: LoginAs, data: Factories, apiAs: ApiAs): Promise<void> {
  await invSupplier(data)
  // STEP 1-3: Pembelian → Permintaan baru, the weekly rice and oil of the kitchen
  await loginAs('admin', '/inventory/purchasing')
  await page.getByTestId('request-new').click()
  const form = page.getByTestId('request-form')
  await expect(form).toBeVisible()
  await selectContaining(form.locator('select[name=department_id]'), 'FB ·')
  await selectContaining(form.locator('select[name=warehouse_id]'), 'UTAMA ·')
  await setDate(form, 'needed_on', addDays(await data.businessDate(), 3))
  await form.locator('input[name=note]').fill('Kebutuhan mingguan dapur')
  await invLine(page, form, 0, 'BERAS', 'karung', '4')
  await form.locator('input[name=price_0]').fill('434000')
  await selectContaining(form.locator('select[name=supplier_0]'), INV_SUPPLIER.name)
  await form.getByTestId('line-add').click()
  await invLine(page, form, 1, 'MGRNG', 'l', '20')
  await form.locator('input[name=price_1]').fill('23500')
  await selectContaining(form.locator('select[name=supplier_1]'), INV_SUPPLIER.name)
  await shot(form, 'inventaris-pr-03')
  // STEP 4: Ajukan untuk persetujuan
  await page.getByTestId('request-submit').click()
  await expect(page).toHaveURL(/\/inventory\/requests\/\d+$/)
  const id = Number(page.url().split('/').pop())
  await expect.poll(async () => (await data.api.get<{ status: string }>(`${data.P}/inventory/requests/${id}`)).status).toBe('SUBMITTED')
  // STEP 5: the chain decides: the head of F&B, then cost control (2,206,000 is under the third level)
  await (await apiAs('invHead')).post(`${data.P}/inventory/requests/${id}/approve`, { note: 'Sesuai menu minggu ini' })
  await (await apiAs('invCost')).post(`${data.P}/inventory/requests/${id}/approve`, { note: 'Harga wajar' })
  await expect.poll(async () => (await data.api.get<{ status: string }>(`${data.P}/inventory/requests/${id}`)).status).toBe('APPROVED')
  // STEP 6: the approved request: the box of the orders, sent to the supplier at once
  await page.reload()
  const box = page.getByTestId('orders-box')
  await expect(box).toBeVisible()
  for (const n of [1, 2]) await selectContaining(box.locator(`select[name=order_supplier_${n}]`), INV_SUPPLIER.name)
  await box.locator('input[name=send]').check()
  await shot(box, 'inventaris-pr-06')
  // STEP 7: Buat 1 pesanan
  await box.getByTestId('orders-make').click()
  await expect(page.getByTestId('notice')).toBeVisible()
  // STEP 8: the order, on its page
  const number = (await data.api.get<{ request_number: string }>(`${data.P}/inventory/requests/${id}`)).request_number
  const po = (await invRows<{ id: number; request_number: string | null }>(data, '/inventory/purchase-orders')).find((o) => o.request_number === number)
  if (!po) throw new Error(`no order made from ${number}`)
  await page.goto(`/inventory/purchase-orders/${po.id}`)
  await expect(page.getByTestId('order-view')).toBeVisible()
  await shotAround([page.locator('main h1'), page.getByTestId('order-view')], 'inventaris-pr-08')
}

/** 09-inventory/penerimaan-tagihan-dan-retur.md (the order of flowPermintaanPembelian) */
export async function flowPenerimaan(page: Page, loginAs: LoginAs, data: Factories): Promise<void> {
  const orders = await invRows<{ id: number; status: string; supplier_name: string }>(data, '/inventory/purchase-orders')
  const po = orders.filter((o) => o.status === 'ORDERED' && o.supplier_name === INV_SUPPLIER.name).sort((a, b) => b.id - a.id)[0]
  if (!po) throw new Error('no order of SUMBER waits for its goods: run "09 permintaan pembelian" first')
  // STEP 1-3: the order → Terima barang, everything came
  await loginAs('admin', `/inventory/purchase-orders/${po.id}`)
  await page.getByTestId('order-receive').click()
  const form = page.getByTestId('receipt-form')
  await expect(form).toBeVisible()
  await expect(form.locator('input[name=qty_0]')).not.toHaveValue('')
  await form.locator('input[name=delivery_note]').fill('SJ-SS-0412')
  await shot(form, 'inventaris-terima-03')
  // STEP 4: Posting penerimaan
  await page.getByTestId('receipt-post').click()
  await expect(page).toHaveURL(/\/inventory\/receipts\/\d+$/)
  await expect(page.getByTestId('receipt-view')).toBeVisible()
  const receiptId = Number(page.url().split('/').pop())
  const receipt = (await data.api.get<{ receipt_number: string }>(`${data.P}/inventory/receipts/${receiptId}`)).receipt_number
  // STEP 5-7: Retur ke pemasok: one wet sack of rice, before the bill
  await page.getByTestId('receipt-return').click()
  const ret = page.getByTestId('return-form')
  await expect(ret).toBeVisible()
  await ret.locator('input[name=reason]').fill('Satu karung beras basah saat diterima')
  await ret.locator('input[name=qty_0]').fill('1')
  await shot(ret, 'inventaris-terima-06')
  await page.getByTestId('return-post').click()
  await expect(page).toHaveURL(/\/inventory\/returns\/\d+$/)
  await expect(page.getByTestId('return-view')).toBeVisible()
  // STEP 8-9: Draf tagihan → the receipt → Tagih yang dipilih, the invoice of the supplier
  await page.goto('/inventory/bill-drafts')
  await page.getByTestId(`pick-${receipt}`).click()
  await page.getByTestId('bill-picked').click()
  await expect(page).toHaveURL(/\/inventory\/bill-drafts\/\d+/)
  await page.getByTestId('invoice-number').fill('INV/SS/1124/118')
  await expect(page.getByTestId('bill-summary')).toBeVisible()
  await shotAround([page.locator('main h1'), page.getByTestId('bill-summary')], 'inventaris-tagihan-09')
  // STEP 10-11: Konfirmasi tagihan (the prices are those of the order: no approval)
  await expect(page.getByTestId('bill-confirm')).toBeEnabled()
  await page.getByTestId('bill-confirm').click()
  await expect(page.getByTestId('notice')).toBeVisible()
}

/** 09-inventory/rantai-persetujuan.md */
export async function flowRantaiPersetujuan(page: Page, loginAs: LoginAs, data: Factories): Promise<void> {
  // STEP 1: Rantai persetujuan, the chain of the purchase requests (shown, not changed)
  await loginAs('admin', '/inventory/approval-chains')
  const chain = page.getByTestId('chain-PR')
  await expect(chain.getByTestId('level-PR-1')).toBeVisible()
  await shot(chain, 'inventaris-rantai-01')
  // a request of the administrator for housekeeping, waiting for its head (it stays at level 2 for the report of the pending documents)
  const supplier = await invSupplier(data)
  const sampo = await invByCode(data, '/inventory/items', 'SAMPO')
  const rooms = await invByCode(data, '/departments', 'ROOMS')
  const hk = await invByCode(data, '/inventory/warehouses', 'HK')
  const pr = await data.api.post<{ id: number; request_number: string }>(`${data.P}/inventory/requests`, {
    client_uuid: crypto.randomUUID(), department_id: rooms.id, warehouse_id: hk.id, note: 'Stok amenities kamar',
    lines: [{ item_id: sampo.id, unit_id: await invUnit(data, sampo.id, 'karton'), qty: '2', est_unit_price: '325000', supplier_id: supplier }],
  })
  await data.api.post(`${data.P}/inventory/requests/${pr.id}/submit`, {})
  // STEP 5: the head of the department: Perlu saya setujui
  await loginAs('invHead', '/inventory/approvals')
  const inbox = page.getByTestId('inbox-table')
  await expect(inbox.getByTestId(`inbox-${pr.request_number}`)).toBeVisible()
  await shotAround([page.locator('main h1'), inbox], 'inventaris-rantai-05')
  // STEP 6-8: the request, its chain and the decision box
  await inbox.getByTestId(`inbox-${pr.request_number}`).click()
  const decision = page.getByTestId('decision-box')
  await expect(decision.getByTestId('decision-approve')).toBeVisible()
  await decision.getByTestId('decision-note').fill('Stok sampo tinggal untuk satu minggu')
  await shot(page.getByTestId('chain-aside'), 'inventaris-rantai-08')
  // STEP 9: Setujui: the request goes to cost control
  await decision.getByTestId('decision-approve').click()
  await expect(page.getByTestId('notice')).toBeVisible()
}

/** 09-inventory/permintaan-barang-dan-transfer.md */
export async function flowPermintaanBarang(page: Page, loginAs: LoginAs, data: Factories, apiAs: ApiAs): Promise<void> {
  // STEP 1-3: Permintaan & transfer → Permintaan baru, the kitchen asks the main store
  await loginAs('admin', '/inventory/requisitions')
  await page.getByTestId('requisition-new').click()
  const form = page.getByTestId('sr-form')
  await expect(form).toBeVisible()
  await selectContaining(form.locator('select[name=to_warehouse_id]'), 'DAPUR ·')
  await selectContaining(form.locator('select[name=from_warehouse_id]'), 'UTAMA ·')
  await setDate(form, 'needed_on', addDays(await data.businessDate(), 1))
  await form.locator('input[name=note]').fill('Persiapan menu akhir pekan')
  await invLine(page, form, 0, 'BERAS', 'kg', '30')
  await form.getByTestId('line-add').click()
  await invLine(page, form, 1, 'TELUR', 'pcs', '120')
  await form.getByTestId('line-add').click()
  await invLine(page, form, 2, 'GULA', 'kg', '10')
  await shot(form, 'inventaris-sr-03')
  // STEP 4: Ajukan untuk persetujuan
  await page.getByTestId('sr-submit').click()
  await expect(page).toHaveURL(/\/inventory\/requisitions\/\d+$/)
  const id = Number(page.url().split('/').pop())
  await expect.poll(async () => (await data.api.get<{ status: string }>(`${data.P}/inventory/requisitions/${id}`)).status).toBe('SUBMITTED')
  // STEP 5: the head of F&B, then the final approver of the store requisitions
  await (await apiAs('invHead')).post(`${data.P}/inventory/requisitions/${id}/approve`, { note: 'Sesuai rencana menu' })
  await (await apiAs('invFinal')).post(`${data.P}/inventory/requisitions/${id}/approve`, {})
  await expect.poll(async () => (await data.api.get<{ status: string }>(`${data.P}/inventory/requisitions/${id}`)).status).toBe('APPROVED')
  await page.reload()
  await expect(page.getByTestId('sr-answer')).toBeVisible()
  await expect(page.getByTestId('chain-aside')).toBeVisible()
  await shotPage(page, 'inventaris-sr-05')
  // STEP 6-7: Jawab dengan transfer: the lines come from the request
  await page.getByTestId('sr-answer').click()
  const trf = page.getByTestId('transfer-form')
  await expect(trf).toBeVisible()
  await expect(trf.locator('input[name=qty_0]')).not.toHaveValue('')
  await shotAround([page.locator('main h1'), trf], 'inventaris-sr-07')
  // STEP 8: Kirim transfer
  await page.getByTestId('transfer-post').click()
  await expect(page).toHaveURL(/\/inventory\/transfers\/\d+$/)
  await expect(page.getByTestId('transfer-view')).toBeVisible()
}

/** 09-inventory/pemakaian-dan-waste.md */
export async function flowPemakaian(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1: Pemakaian & waste
  await loginAs('admin', '/inventory/issues')
  await expect(page.getByTestId('issues-table')).toBeVisible()
  await shotAround([page.locator('main h1'), page.getByTestId('issues-table')], 'inventaris-pakai-01')
  // STEP 2-4: Catat waste: dried noodles past their date in the kitchen store (no recipe uses them, so the stock is the opening's)
  await page.getByTestId('issue-waste').click()
  const form = page.getByTestId('issue-form')
  await expect(form).toBeVisible()
  await expect(page.getByTestId('purpose-WASTE')).toHaveAttribute('aria-selected', 'true')
  await selectContaining(form.locator('select[name=warehouse_id]'), 'DAPUR ·')
  await form.locator('select[name=reason_code]').selectOption('EXPIRED')
  await form.locator('input[name=note]').fill('Mie kering lewat tanggal kedaluwarsa')
  await invLine(page, form, 0, 'MIE', null, '2')
  await expect(form.getByTestId('too-much-0')).toHaveCount(0)
  await shot(form, 'inventaris-pakai-04')
  // STEP 5: Posting waste: the document opens
  await page.getByTestId('issue-post').click()
  await expect(page).toHaveURL(/\/inventory\/issues\/\d+$/)
  await expect(page.getByTestId('issue-view')).toBeVisible()
  await shotAround([page.locator('main h1'), page.getByTestId('issue-view')], 'inventaris-pakai-05')
}

/** 09-inventory/opname.md: the count of the bar is approved (or cancelled) before the flow ends, or it would hold the night audit. */
export async function flowOpname(page: Page, loginAs: LoginAs, data: Factories): Promise<void> {
  let id = 0
  try {
    // STEP 1-2: Opname → Mulai opname, the whole bar
    await loginAs('admin', '/inventory/counts')
    await page.getByTestId('count-start-open').click()
    const form = page.getByTestId('count-form')
    await selectContaining(form.locator('select[name=warehouse_id]'), 'BAR ·')
    await form.locator('select[name=scope]').selectOption('ALL')
    await form.locator('input[name=note]').fill('Opname mingguan bar')
    await shot(form, 'inventaris-opname-02')
    // STEP 3: Mulai dan bekukan: the count sheet
    await page.getByTestId('count-start').click()
    await expect(page).toHaveURL(/\/inventory\/counts\/\d+$/)
    id = Number(page.url().split('/').pop())
    // STEP 4: what the shelf holds, saved when the box is left
    for (const [code, qty] of [['BIR', '46'], ['SODA', '22']] as const) {
      const box = page.locator(`input[name=count_${code}]`)
      await expect(box).toBeVisible()
      await box.fill(qty)
      const saved = page.waitForResponse((r) => r.url().includes(`/inventory/counts/${id}/lines`) && r.request().method() === 'PUT')
      await box.press('Tab')
      await saved
    }
    await page.getByTestId('tab-done').click()
    await expect(page.locator('input[name=count_BIR]')).toBeVisible()
    await shotPage(page, 'inventaris-opname-04')
    // STEP 5: Kirim untuk disetujui
    await page.getByTestId('count-submit').click()
    await expect(page.getByTestId('notice')).toBeVisible()
    // STEP 6-7: the approver reviews and approves
    await loginAs('invApprover', `/inventory/counts/${id}`)
    await expect(page.getByTestId('count-review')).toBeVisible()
    await shotPage(page, 'inventaris-opname-06')
    await page.getByTestId('count-approve').click()
    await expect(page.getByTestId('notice')).toBeVisible()
  } finally {
    if (id) {
      const st = (await data.api.get<{ status: string }>(`${data.P}/inventory/counts/${id}`)).status
      if (st === 'OPEN' || st === 'SUBMITTED') await data.api.post(`${data.P}/inventory/counts/${id}/cancel`, {})
    }
    // no count of this run may stay open (the night audit of the final project)
    for (const c of await invRows<{ id: number }>(data, '/inventory/counts', { status: 'OPEN' })) await data.api.soft('POST', `${data.P}/inventory/counts/${c.id}/cancel`, {})
  }
}

/** 09-inventory/resep.md */
export async function flowResep(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1-3: POS → Produk dan menu → the fried rice: its recipe and the cost of its ingredients (pms-seed inventory)
  await loginAs('admin', '/pos/products')
  await page.getByTestId('product-NASGOR').click()
  const recipe = page.getByTestId('product-recipe')
  await expect(recipe.getByTestId('recipe-summary')).toBeVisible()
  await tallWindow(page, 2400, () => shot(recipe, 'inventaris-resep-03')) // the recipe is taller than the window
  // STEP 7: POS → Outlet → Ubah on the restaurant: the cost targets
  await page.goto('/pos/outlets')
  await page.getByTestId('outlet-edit-RESTO').click()
  const dialog = page.getByTestId('outlet-dialog')
  const targets = dialog.getByTestId('outlet-targets')
  await expect(targets).toBeVisible()
  await targets.getByTestId('target-FOOD').fill('32')
  await targets.getByTestId('target-BEVERAGE').fill('25') // shown, not saved (Escape below)
  await tallWindow(page, 1800, () => shotAround([dialog.getByTestId('outlet-stock'), targets], 'inventaris-resep-07'))
  await page.keyboard.press('Escape')
}

/** 09-inventory/amenities-otomatis.md */
export async function flowAmenities(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1-3: Amenities otomatis: the plan of pms-seed inventory (shown, not saved: a save would make a new version)
  await loginAs('admin', '/inventory/amenities')
  await expect(page.getByTestId('plan-form')).toBeVisible()
  await expect(page.getByTestId('plan-lines')).toBeVisible()
  await expect(page.getByTestId('runs-box')).toBeVisible()
  await shotPage(page, 'inventaris-amenities-03')
  // STEP 6: the document of the night the world's audit closed (none when that audit could not run)
  const run = page.getByTestId('runs-box').locator('a[data-testid^="run-"]').first()
  if (await run.count()) {
    await run.click()
    await expect(page.getByTestId('run-view')).toBeVisible()
    await shotPage(page, 'inventaris-amenities-06')
  }
}

/** 09-inventory/laporan-persediaan.md */
export async function flowLaporanPersediaan(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1: Persediaan → Ringkasan
  await loginAs('admin', '/inventory')
  await expect(page.getByTestId('dashboard')).toBeVisible()
  await expect(page.getByTestId('reports-list')).toBeVisible()
  await shotPage(page, 'inventaris-laporan-01')
  // STEP 2-3: Nilai stok → Tampilkan
  await page.getByTestId('report-value').click()
  await expect(page).toHaveURL(/\/inventory\/reports\/stock-value/)
  await page.getByTestId('report-show').click()
  await expect(page.getByTestId('value-table').or(page.getByTestId('value-empty')).first()).toBeVisible()
  await tallWindow(page, 1800, () => shotAround([page.locator('main h1'), page.getByTestId('report-period'), page.getByTestId('value-table').or(page.getByTestId('value-empty')).first()], 'inventaris-laporan-03'))
  // STEP 4: Barang di bawah minimum: the pool chlorine of "09 barang gudang dan saldo awal" (40 kg under a minimum of 50)
  await page.goto('/inventory/reports/low-stock')
  const low = page.getByTestId('low-table')
  await expect(low.getByTestId(`low-UTAMA-${INV_ITEM.code}`)).toBeVisible()
  await low.getByTestId(`low-check-UTAMA-${INV_ITEM.code}`).click()
  await expect(page.getByTestId('low-selected')).toBeVisible()
  await shotAround([page.locator('main h1'), low], 'inventaris-laporan-04')
}


// =====================================================================================================================
// 11-audit-malam and 12-laporan. No seeded role holds nightaudit.* or audit.read: the administrator works the night audit, its pack and the
// audit trail; the supervisor (report.view, cashier.shift_manage) reads the reports. The order of the tests (docs-screenshots.spec.ts) matters:
//   (A) on 2026-11-24, before the run: the reports, the pack archive and its recipients, the screen with its blockers, then the no-shows;
//   (B) flowJalankanAuditMalam clears what still holds the day, checks in a guest who stays over the audit and closes 2026-11-24.
//   The early departure of that guest after the audit (KD-06) is the permanent test NA-04 in e2e/final/night-audit.spec.ts.

/** The preview of the night audit as far as these flows read it. */
interface NaPreview {
  business_date: string
  can_run: boolean
  time_guard_ok?: boolean
  blockers: {
    unresolved_arrivals?: { reservation_room_id: number; confirmation_number: string; guest: string }[]
    unresolved_departures?: { stay_id: number; stay_number: string; guest: string }[]
    charge_errors?: unknown[]
    invalid_charges?: unknown[]
    open_shifts?: { id: number; number: string; drawer: string; user_name: string }[]
    accounting_readiness?: { code: string; message: string }[]
    outlet_bills?: { code: string; message: string }[]
    stock?: { code: string; message: string; ref?: string }[]
  }
  warnings?: Record<string, unknown>
}

async function naPreview(data: Factories): Promise<NaPreview> {
  return data.api.get<NaPreview>(`${data.P}/night-audit/preview`)
}

/** What holds the day, in one line of the run log. */
function naSummary(p: NaPreview): string {
  const b = p.blockers ?? {}
  const n = (x?: unknown[]) => x?.length ?? 0
  return `can_run=${p.can_run} time=${p.time_guard_ok !== false} arrivals=${n(b.unresolved_arrivals)} [${(b.unresolved_arrivals ?? []).map((a) => a.guest).join(', ')}]`
    + ` departures=${n(b.unresolved_departures)} [${(b.unresolved_departures ?? []).map((d) => d.guest).join(', ')}] charge_errors=${n(b.charge_errors)}`
    + ` invalid=${n(b.invalid_charges)} shifts=${n(b.open_shifts)} accounting=${(b.accounting_readiness ?? []).map((a) => a.code).join(',') || 0}`
    + ` outlets=${n(b.outlet_bills)} stock=${(b.stock ?? []).map((s) => s.code).join(',') || 0}`
}

/** Akhir hari → Audit malam, as the administrator, read. */
async function openNightAudit(page: Page, loginAs: LoginAs): Promise<void> {
  await loginAs('admin', '/night-audit')
  await expect(page.getByTestId('audit-date')).toBeVisible()
  await expect(page.getByTestId('run')).toBeVisible()
}

/** Step 2 of the page: ticks every arrival that is left (Pilih semua), the reason and the statement, then Tandai {n} sebagai no-show. */
async function markNoShows(page: Page, reason: string, file?: string): Promise<boolean> {
  const arrivals = page.getByTestId('arrivals')
  if (!(await arrivals.locator('[data-testid^="arrival-"]').count())) return false
  await arrivals.getByTestId('select-all').check()
  const form = page.getByTestId('noshow-form')
  await form.locator('input[name=reason]').fill(reason)
  await form.locator('input[name=confirm]').check()
  await expect(page.getByTestId('mark-no-shows')).toBeEnabled()
  if (file) await shot(arrivals, file)
  await page.getByTestId('mark-no-shows').click()
  await expect(page.getByTestId('notice')).toBeVisible()
  await expect(page.getByTestId('no-arrivals')).toBeVisible()
  return true
}

// --- 12-laporan

/** 12-laporan/laporan-front-office.md */
export async function flowLaporanFrontOffice(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1: Akhir hari → Laporan (Sari Supervisor: report.view)
  await loginAs('supervisor', '/reports')
  const form = page.getByTestId('filters')
  await expect(form).toBeVisible()
  // STEP 2: Laporan Kedatangan; Tanggal is the business date already
  await form.locator('select[name=report]').selectOption('arrivals')
  await expect(form.locator('input[name=date]')).toBeAttached()
  await shot(form, 'laporan-fo-02')
  // STEP 3: Jalankan: the table of the arrivals of today
  await form.getByTestId('run').click()
  const result = page.getByTestId('result')
  await expect(result).toBeVisible()
  await shot(result, 'laporan-fo-03')
  // STEP 4: Unduh CSV is offered once a result is shown (not clicked: the file would go to the downloads)
  await expect(form.getByTestId('csv')).toBeEnabled()
  // STEP 5: another report: Okupansi dan statistik over the default seven days (only the closed days have a row)
  await form.locator('select[name=report]').selectOption('statistics')
  await form.getByTestId('run').click()
  await expect(result).toBeVisible()
  await tallWindow(page, 1200, () => shot(result, 'laporan-fo-05'), 1920) // the statistics table is wider than 1440
}

/** 12-laporan/laporan-harian.md */
export async function flowLaporanHarian(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1: Akhir hari → Laporan: the card Laporan harian (the supervisor also sees the Z report)
  await loginAs('supervisor', '/reports')
  const hub = page.getByTestId('day-reports')
  await expect(hub.getByTestId('day-report-flash')).toBeVisible()
  await shot(hub, 'laporan-harian-01')
  // STEP 2: Daily flash report: it shows itself for the last closed day
  await hub.getByTestId('day-report-flash').click()
  await expect(page).toHaveURL(/\/reports\/daily-flash/)
  await expect(page.getByTestId('flash-summary')).toBeVisible({ timeout: 30_000 })
  // STEP 3: the cards and the room statistics
  await expect(page.getByTestId('flash-rooms')).toBeVisible()
  await tallWindow(page, 1600, () => shotAround([page.locator('main h1'), page.getByTestId('flash-rooms')], 'laporan-harian-03'))
  // STEP 4: PDF and Unduh CSV are offered (not clicked: a new tab, a download)
  await expect(page.getByTestId('pdf')).toBeEnabled()
  // STEP 5: Guest ledger of the last closed day, with the check against the ledger
  await page.goto('/reports/guest-ledger')
  await expect(page.getByTestId('guest-ledger')).toBeVisible()
  await expect(page.getByTestId('gl-table').or(page.getByTestId('empty')).or(page.getByTestId('report-error')).first()).toBeVisible({ timeout: 30_000 })
  const checks = page.getByTestId('ledger-checks')
  if (await checks.count()) await shotAround([page.locator('main h1'), checks], 'laporan-harian-05')
  else await shotPage(page, 'laporan-harian-05')
  // STEP 6: the form below the card: Ringkasan harian of today (run, not shot)
  await page.goto('/reports')
  const form = page.getByTestId('filters')
  await form.locator('select[name=report]').selectOption('daily-summary')
  await form.getByTestId('run').click()
  await expect(page.getByTestId('result').or(page.getByTestId('form-error')).first()).toBeVisible()
}

/** 12-laporan/kinerja-dan-dasbor.md */
export async function flowKinerja(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1: Akhir hari → Kinerja
  await loginAs('supervisor', '/performance')
  await expect(page.getByTestId('main-kpis')).toBeVisible()
  await expect(page.getByTestId('forecast')).toBeVisible()
  await shotPage(page, 'laporan-kinerja-01')
  // STEP 2: Periode → Bulan berjalan: the four cards follow
  await page.getByTestId('period-month').click()
  await expect(page.getByTestId('kpi-occupancy')).toBeVisible()
  await shotAround([page.getByTestId('period'), page.getByTestId('main-kpis')], 'laporan-kinerja-02')
  // STEP 4: the 14 nights ahead
  const forecast = page.getByTestId('forecast')
  await shot(forecast, 'laporan-kinerja-04')
  // STEP 5: Muat ulang
  await page.getByTestId('dash-refresh').click()
  await expect(page.getByTestId('main-kpis')).toBeVisible()
}

/** 12-laporan/jejak-audit.md */
export async function flowJejakAudit(page: Page, loginAs: LoginAs, w: DocsWorld): Promise<void> {
  const entries = page.locator('[data-testid^="entry-"]')
  // STEP 1: Pengaturan → Jejak audit (the administrator: no seeded role holds audit.read)
  await loginAs('admin', '/audit')
  const filters = page.getByTestId('filters')
  await expect(filters).toBeVisible()
  await expect(entries.first()).toBeVisible()
  await shotPage(page, 'laporan-jejak-01')
  // STEP 2-3: Nomor dokumen = the reservation of Yusuf Maulana (cancelled with a fee in 01 membatalkan reservasi), Cari
  await filters.locator('input[name=document]').fill(w.future.yusuf.confirmation)
  await page.getByTestId('search').click()
  await expect(entries.first()).toBeVisible()
  await shotAround([filters, entries.last()], 'laporan-jejak-03')
  // STEP 4: Detail of the newest entry: Sebelum and Sesudah
  // the first entry row (the side menu also has toggle-* test ids, so read the id from the row)
  const id = ((await entries.first().getAttribute('data-testid')) ?? '').replace('entry-', '')
  await page.getByTestId(`toggle-${id}`).first().click()
  const detail = page.getByTestId(`detail-${id}`)
  await expect(detail).toBeVisible()
  await shotAround([page.getByTestId(`entry-${id}`), detail], 'laporan-jejak-04')
}

// --- 11-audit-malam (A): screens that do not close the day

/** 11-audit-malam/paket-audit-malam.md, steps 1-6: the archive and the pack of the day the docs world closed. */
export async function flowArsipPaket(page: Page, loginAs: LoginAs, data: Factories): Promise<void> {
  const closed = addDays(await data.businessDate(), -1)
  // STEP 1: Akhir hari → Arsip audit malam
  await loginAs('admin', '/night-audit/archive')
  const row = page.getByTestId(`pack-row-${closed}`)
  await expect(row.or(page.getByTestId('late')).first()).toBeVisible({ timeout: 60_000 })
  await shotAround([page.locator('main h1'), page.getByTestId('packs')], 'auditmalam-paket-01')
  if (!(await row.count())) {
    console.warn(`[docs] no night audit pack for ${closed}: the docs world did not close the day of setup`)
    return
  }
  // STEP 3: Buka on the row of the day: its sections and their state
  await page.getByTestId(`open-${closed}`).click()
  const detail = page.getByTestId('detail')
  await expect(detail.getByTestId('section-SUMMARY')).toBeVisible()
  // STEP 4: a section ticked under Cetak makes PDF bagian yang dipilih available (the PDF itself opens in a new tab: not opened)
  const pick = detail.getByTestId('section-SUMMARY').locator('input[type=checkbox]')
  if (await pick.isEnabled()) {
    await pick.check()
    await expect(detail.getByTestId('pdf-chosen')).toBeEnabled()
  }
  await shot(detail, 'auditmalam-paket-03')
  // STEP 5: Riwayat pengiriman (e-mail is off on the E2E server)
  await expect(page.getByTestId('history')).toBeVisible()
}

/** 11-audit-malam/paket-audit-malam.md, steps 7-9: the recipients of the pack (pms-seed nightaudit made five). */
export async function flowPenerimaPaket(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 7: Akhir hari → Penerima paket
  await loginAs('admin', '/night-audit/recipients')
  await expect(page.getByTestId('recipients')).toBeVisible()
  await expect(page.getByTestId('email-state')).toBeVisible()
  // STEP 8: Tambah penerima: the address, the name, two more sections
  await page.getByTestId('add-recipient').click()
  const dialog = page.getByTestId('recipient-dialog')
  await dialog.locator('input[name=email]').fill('night.manager@hotelkamara.example')
  await dialog.locator('input[name=name]').fill('Night manager')
  await dialog.locator('input[name=section_IN_HOUSE]').check()
  await dialog.locator('input[name=section_NEXT_DAY]').check()
  await expect(dialog.getByTestId('save-recipient')).toBeEnabled()
  await shot(dialog, 'auditmalam-paket-08')
  // STEP 9: Simpan is not clicked: the list of the pictures stays the one of the demo data
  await page.keyboard.press('Escape')
  await expect(dialog).toHaveCount(0)
}

/** 11-audit-malam/menangani-penghalang.md: the screen with what holds 2026-11-24 after the earlier docs flows. */
export async function flowPenghalang(page: Page, loginAs: LoginAs, data: Factories): Promise<void> {
  const preview = await naPreview(data)
  console.log(`[docs] night audit ${preview.business_date} before the no-shows: ${naSummary(preview)}`)
  // STEP 1: Akhir hari → Audit malam: the card Jalankan lists why the day cannot close
  await openNightAudit(page, loginAs)
  const run = page.getByTestId('run')
  if (!preview.can_run) await expect(run.getByTestId('cannot-run')).toBeVisible()
  await shot(run, 'auditmalam-penghalang-01')
  // STEP 2: each reason is a link to its step (not clicked: the steps are shot one by one)
  // STEP 3: Keberangkatan yang belum selesai, with Bayar & check-out and Perpanjang on the row
  const departures = page.getByTestId('departures')
  await expect(departures).toBeVisible()
  await shot(departures, 'auditmalam-penghalang-03')
  // STEP 6: the card Peringatan (only when the day has warnings)
  const warnings = page.getByTestId('warnings')
  if (await warnings.count()) await shot(warnings, 'auditmalam-penghalang-06')
  else console.warn('[docs] the night audit shows no warnings: auditmalam-penghalang-06 keeps the picture of the last run')
}

/** 11-audit-malam/menandai-no-show.md: every arrival of the day still unresolved is marked no-show the way the screen does it. */
export async function flowNoShow(page: Page, loginAs: LoginAs, data: Factories): Promise<void> {
  // STEP 1: step 2 of Audit malam, Kedatangan yang belum selesai
  await openNightAudit(page, loginAs)
  const arrivals = page.getByTestId('arrivals')
  if (!(await arrivals.locator('[data-testid^="arrival-"]').count())) {
    console.warn('[docs] no arrival is left to mark no-show: the pictures of menandai-no-show keep those of the last run')
    return
  }
  await shot(arrivals, 'auditmalam-noshow-01')
  // STEP 3-4: Pilih semua, the reason and the statement (shot), then Tandai {n} sebagai no-show
  await markNoShows(page, 'Tidak datang sampai pukul 23.00, telepon tidak dijawab', 'auditmalam-noshow-03')
  await shotAround([page.getByTestId('notice'), page.getByTestId('no-arrivals')], 'auditmalam-noshow-04')
  const left = await naPreview(data)
  console.log(`[docs] night audit after the no-shows: ${naSummary(left)}`)
}

// --- 11-audit-malam (B): the run that closes 2026-11-24

/** The guest of the early departure of (C): arrives on the business date of (B) for three nights, checked in before the run, no extra charge. */
const PULANG_AWAL = { first_name: 'Rahmat', last_name: 'Pulangawal' }
let pulangAwal: { stayId: number; folioId: number } | null = null

async function findPulangAwal(data: Factories): Promise<{ stayId: number; folioId: number } | null> {
  if (pulangAwal) return pulangAwal
  const rows = (await data.api.get<{ data: { id: number }[] }>(`${data.P}/stays/in-house`, { q: PULANG_AWAL.last_name })).data
  const id = rows[0]?.id
  if (!id) return null
  const st = await data.stay(id)
  const folio = st.folios[0]
  if (!folio) return null
  pulangAwal = { stayId: id, folioId: folio.id }
  return pulangAwal
}

async function checkInPulangAwal(data: Factories): Promise<void> {
  if (await findPulangAwal(data)) return // a second run on the same database
  const guest = await data.guest({ ...PULANG_AWAL, phone: '+62 812 4400 1515' })
  const res = await data.reservation({ nights: 3, guest })
  const full = await data.api.get<{ version: number; rooms: { id: number }[] }>(`${data.P}/reservations/${res.id}`)
  const out = await data.api.post<{ stay: { id: number }; folio: { id: number } }>(`${data.P}/reservations/${res.id}/rooms/${full.rooms[0]!.id}/check-in`,
    { version: full.version, guest_id: guest.id, adult_count: 2, room_id: res.room!.id })
  pulangAwal = { stayId: out.stay.id, folioId: out.folio.id }
  console.log(`[PULANGAWAL] checked in on ${res.arrival_date} until ${res.departure_date}: stay ${out.stay.id}, folio ${out.folio.id}`)
}

/** Step 3 of the page: every unfinished departure is checked out in the side sheet (paid by card first when the guest owes). */
async function checkOutDepartures(page: Page, data: Factories): Promise<void> {
  const tried = new Set<number>()
  for (let i = 0; i < 20; i++) {
    const rows = page.getByTestId('departures').locator('tr[data-testid^="departure-"]')
    const ids: number[] = []
    for (const t of await rows.evaluateAll((els) => els.map((e) => e.getAttribute('data-testid') ?? ''))) ids.push(Number(t.replace('departure-', '')))
    const stayId = ids.find((id) => !tried.has(id))
    if (stayId === undefined) return
    tried.add(stayId)
    await page.getByTestId(`check-out-${stayId}`).click()
    const sheet = page.getByTestId('departure-sheet')
    const wizard = sheet.getByTestId('checkout-wizard')
    await expect(wizard.getByTestId('step-departure')).toHaveAttribute('data-settled', 'true') // the server's preview has said whether it is early
    const early = wizard.locator('input[name=confirm_early]')
    if (await early.count()) await early.check()
    // each guest folio that owes money: Bayar by card, then the wizard reads the stay again
    for (let k = 0; k < 4; k++) {
      const owing = wizard.locator('[data-testid^="folio-due-"]').filter({ has: page.getByTestId('checkout-pay') })
      if (!(await owing.count())) break
      const box = owing.first()
      const folio = ((await box.getAttribute('data-testid')) ?? '').replace('folio-due-', '')
      const pay = box.getByTestId('checkout-pay')
      await pay.locator('select[name=pay_method]').selectOption('CARD')
      await pay.locator('input[name=pay_reference]').fill(`EDC 0046${10 + k}`)
      await pay.getByTestId('checkout-pay-submit').click()
      await expect(wizard.getByTestId(`folio-due-${folio}`).getByTestId('checkout-pay')).toHaveCount(0, { timeout: 15_000 })
    }
    const submit = wizard.getByTestId('checkout-submit')
    if (await submit.isEnabled()) {
      await submit.click()
      await expect(sheet).toHaveCount(0, { timeout: 15_000 })
      await expect(page.getByTestId(`departure-${stayId}`)).toHaveCount(0, { timeout: 15_000 })
      continue
    }
    // what the wizard cannot do (a credit to refund, a company folio): said in the run log, the API tries the check-out of the screen
    const reason = (await wizard.getByTestId('checkout-due-reason').count()) ? await wizard.getByTestId('checkout-due-reason').innerText() : ''
    console.warn(`[docs] the check-out of stay ${stayId} is not offered on the screen (${reason}): tried through the API`)
    await page.keyboard.press('Escape')
    const st = await data.stay(stayId)
    for (const f of st.folios) await data.settle(f.id).catch(() => undefined)
    await data.api.post(`${data.P}/stays/${stayId}/check-out`, { version: st.stay.version, confirm_early_departure: true })
      .catch((e: unknown) => console.warn(`[docs] the check-out of stay ${stayId} was refused: ${e instanceof Error ? e.message : String(e)}`))
    await page.getByTestId('refresh').click()
    await expect(page.getByTestId('refresh')).toBeEnabled()
  }
}

/** Step 5 of the page: the shifts still open are closed with the cash they expect (closing another cashier's shift is not offered on the screen). */
async function closeOpenShifts(data: Factories, p: NaPreview): Promise<void> {
  for (const sh of p.blockers.open_shifts ?? []) {
    try {
      const d = await data.api.get<{ cash?: { expected: string } }>(`${data.P}/cashier/shifts/${sh.id}`)
      await data.api.post(`${data.P}/cashier/shifts/${sh.id}/close`, { counted_cash: d.cash?.expected ?? '0', reason: 'Ditutup sebelum audit malam' })
      console.log(`[docs] closed the cashier shift ${sh.number} of ${sh.user_name} (${sh.drawer})`)
    } catch (e) {
      console.warn(`[docs] the shift ${sh.number} could not be closed: ${e instanceof Error ? e.message : String(e)}`)
    }
  }
}

/** An open or submitted inventory count freezes its warehouse and may hold the close: cancelled (the docs run counts nothing). */
async function cancelOpenCounts(data: Factories): Promise<void> {
  for (const status of ['OPEN', 'SUBMITTED']) {
    const counts = (await data.api.soft<{ data: { id: number; cnt_number: string }[] }>('GET', `${data.P}/inventory/counts?status=${status}`))?.data ?? []
    for (const c of counts) {
      await data.api.soft('POST', `${data.P}/inventory/counts/${c.id}/cancel`, {})
      console.log(`[docs] cancelled the inventory count ${c.cnt_number} (${status})`)
    }
  }
}

/** 11-audit-malam/menjalankan-audit-malam.md: closes 2026-11-24 from the screen. Runs after every other docs flow of that day. */
export async function flowJalankanAuditMalam(page: Page, loginAs: LoginAs, data: Factories): Promise<void> {
  const bd = await data.businessDate()
  // a guest in house over the audit, three nights from today (the pictures of the run show a guest in house)
  await checkInPulangAwal(data)
  await cancelOpenCounts(data)
  await closeOpenShifts(data, await naPreview(data))
  console.log(`[docs] night audit ${bd} before the run: ${naSummary(await naPreview(data))}`)

  // STEP 1: Akhir hari → Audit malam
  await openNightAudit(page, loginAs)
  // STEP 3: what still holds the day is done on the page: the arrivals (no-show), the departures (check-out in the sheet)
  await markNoShows(page, 'Tidak datang sampai audit malam')
  await checkOutDepartures(page, data)
  await page.getByTestId('refresh').click()
  await expect(page.getByTestId('refresh')).toBeEnabled()
  const preview = await naPreview(data)
  if (!preview.can_run) throw new Error(`the night audit of ${bd} cannot run: ${naSummary(preview)} ${JSON.stringify(preview.blockers).slice(0, 1500)}`)
  await expect(page.getByTestId('cannot-run')).toHaveCount(0)
  await expect(page.getByTestId('readiness')).toBeVisible()
  // STEP 2: every step in order (the whole page, from the top)
  await page.evaluate(() => window.scrollTo(0, 0))
  await closeToasts(page)
  await shotPage(page, 'auditmalam-jalankan-02')
  // STEP 5: Tutup {date} dan buka tanggal bisnis berikutnya
  const run = page.getByTestId('run')
  await run.locator('input[name=confirm_run]').check()
  await expect(page.getByTestId('run-audit')).toBeEnabled()
  await closeToasts(page)
  await shot(run, 'auditmalam-jalankan-05')
  // STEP 6: Jalankan audit malam
  await page.getByTestId('run-audit').click()
  // STEP 7: the result: the day closed, the new business date, the summary and the pack
  const result = page.getByTestId('result')
  await expect(result).toBeVisible({ timeout: 120_000 })
  await expect(result.getByTestId('new-date')).toBeVisible()
  await expect(result.getByTestId('pack')).toBeVisible({ timeout: 30_000 }).catch(() => undefined) // the pack strip follows a moment later
  await page.evaluate(() => window.scrollTo(0, 0))
  await shot(result, 'auditmalam-jalankan-07')
  const next = await data.businessDate()
  expect(next).toBe(addDays(bd, 1))
  console.log(`[docs] night audit closed ${bd}; the business date is ${next}`)
}

// =====================================================================================================================
// 13-pengaturan and 14-admin-platform. The tenant administrator: no E2E role holds room.manage, billing_config.manage, rate.manage or
// property.manage. Nothing the other pictures read is renamed or switched off: what is new gets a name of its own (the role Kasir malam,
// the user Dewi Anggraeni, the charge code SPA, the rate plan BB, the yield rule PENUH90), and the forms of what must stay are shown unsaved.

/** A card of a setup page by its title. */
function setupCard(page: Page, title: string): Locator {
  return page.locator('main [data-slot=card]').filter({ has: page.locator('[data-slot=card-title]', { hasText: title }) }).first()
}

/** The example role of 13-pengaturan: made through the screen by flowPeranPengaturan, by the API in flowPengguna when that test did not run. */
const NIGHT_CASHIER = {
  name: 'Kasir malam',
  description: 'Shift kasir malam: tagihan, pembayaran, dan laporan',
  permissions: ['reservation.read', 'guest.read', 'folio.read', 'folio.post_charge', 'payment.post', 'cashier.shift', 'report.view'],
}
/** The example user of 13-pengaturan (the passwords are made up for the guide). */
const NIGHT_CASHIER_USER = { email: 'dewi.kasir@hotelkamara.example', fullName: 'Dewi Anggraeni', password: 'kopi-pagi-sawah-hijau', reset: 'teh-hangat-sore-hari' }

/** 13-pengaturan/properti.md: the form of a new property is filled and left unsaved (a new property becomes the current one of every later picture). */
export async function flowProperti(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1: Pengaturan → Properti
  await loginAs('admin', '/setup/properties')
  const list = page.locator('main table').first()
  await expect(list.getByRole('link').first()).toBeVisible()
  await shotAround([page.locator('main h1').first(), list], 'pengaturan-properti-01')
  // STEP 2-5: Properti baru: identity, time and money, and the two cards of a new property
  await page.getByRole('button', { name: tx('properties.new'), exact: true }).click()
  await expect(page).toHaveURL(/\/setup\/properties\/new$/)
  const form = page.locator('main form').first()
  await form.locator('input[name=code]').fill('SANUR')
  await form.locator('input[name=name]').fill('Hotel Kamara Sanur')
  await form.locator('input[name=address]').fill('Jl. Danau Tamblingan No. 50')
  await form.locator('input[name=city]').fill('Denpasar')
  await form.locator('input[name=country_code]').fill('ID')
  await form.locator('input[name=timezone]').fill('Asia/Makassar')
  await form.locator('select[name=data_language]').selectOption('id')
  const day = setupCard(page, tx('propertyForm.businessDay'))
  const language = page.getByTestId('data-language-card')
  await day.scrollIntoViewIfNeeded()
  await language.scrollIntoViewIfNeeded()
  await shotAround([day, language], 'pengaturan-properti-05')
  await form.getByRole('button', { name: tx('common.cancel'), exact: true }).click()
  await expect(page).toHaveURL(/\/setup\/properties$/)
  // STEP 7-8: the hotel's own property: its currency is locked by the first financial records (shown, not saved)
  await page.locator('main table').first().getByRole('link').first().click()
  await expect(page.getByTestId('currency-locked')).toBeVisible()
  await shot(setupCard(page, tx('propertyForm.timeMoney')), 'pengaturan-properti-08')
}

/** 13-pengaturan/peran.md */
export async function flowPeranPengaturan(page: Page, loginAs: LoginAs, data: Factories): Promise<void> {
  const roles = (await data.api.get<{ data: { id: number; name: string }[] }>('/roles')).data
  // STEP 1: Pengaturan → Peran
  await loginAs('admin', '/setup/roles')
  if (!roles.some((r) => r.name === NIGHT_CASHIER.name)) {
    // STEP 2: Peran baru, its name and description
    await page.getByRole('button', { name: tx('roles.new'), exact: true }).click()
    await expect(page).toHaveURL(/\/setup\/roles\/new$/)
    await page.locator('input[name=name]').fill(NIGHT_CASHIER.name)
    await page.locator('input[name=description]').fill(NIGHT_CASHIER.description)
    // STEP 3: open the groups and tick the permissions of the night cashier
    for (const group of ['Reservations', 'Guests', 'Billing', 'Reports']) await page.getByTestId(`group-toggle-${group}`).click()
    for (const code of NIGHT_CASHIER.permissions) await page.locator(`[data-permission="${code}"]`).check()
    await shot(page.getByTestId('group-Billing'), 'pengaturan-peran-03')
    // STEP 4: the bar at the bottom of the screen: Buat peran
    const bar = page.getByTestId('save-bar')
    await expect(bar).toBeVisible()
    await shot(bar, 'pengaturan-peran-04')
    await bar.getByTestId('save').click()
    await expect(page).toHaveURL(/\/setup\/roles$/)
  }
  // STEP 5: the list, with the new role and its number of permissions
  const table = page.locator('main table').first()
  await expect(table.getByRole('link', { name: NIGHT_CASHIER.name, exact: true })).toBeVisible()
  await shotAround([page.locator('main h1').first(), table], 'pengaturan-peran-05')
}

/** 13-pengaturan/pengguna.md */
export async function flowPengguna(page: Page, loginAs: LoginAs, data: Factories): Promise<void> {
  const roles = (await data.api.get<{ data: { id: number; name: string }[] }>('/roles')).data
  if (!roles.some((r) => r.name === NIGHT_CASHIER.name)) await data.api.post('/roles', NIGHT_CASHIER)
  const exists = (await data.api.all<{ email: string }>('/users')).some((u) => u.email.toLowerCase() === NIGHT_CASHIER_USER.email)
  // STEP 1: Pengaturan → Pengguna, the column Akses
  await loginAs('admin', '/setup/users')
  const table = page.locator('main table').first()
  await expect(table.getByRole('link').first()).toBeVisible()
  await shotAround([page.locator('main h1').first(), table], 'pengaturan-pengguna-01')
  if (!exists) {
    // STEP 2: Pengguna baru, the profile
    await page.getByRole('button', { name: tx('users.new'), exact: true }).click()
    await expect(page).toHaveURL(/\/setup\/users\/new$/)
    await page.locator('input[name=email]').fill(NIGHT_CASHIER_USER.email)
    await page.locator('input[name=full_name]').fill(NIGHT_CASHIER_USER.fullName)
    await page.locator('input[name=password]').fill(NIGHT_CASHIER_USER.password)
    // STEP 3: Tambah properti: the property (the only one: option 0 is the placeholder) and the role
    await page.getByRole('button', { name: tx('users.addProperty'), exact: true }).click()
    await page.getByLabel(tx('users.propertyN', { n: 1 }), { exact: true }).selectOption({ index: 1 })
    await page.getByLabel(tx('users.roleN', { n: 1 }), { exact: true }).selectOption({ label: NIGHT_CASHIER.name })
    await shot(page.locator('main [data-slot=card]').first(), 'pengaturan-pengguna-03')
    // STEP 4: Buat pengguna
    await page.getByRole('button', { name: tx('users.create'), exact: true }).click()
    await expect(page).toHaveURL(/\/setup\/users$/)
  }
  // STEP 5: the user's page
  await page.locator('main table').first().getByRole('link', { name: NIGHT_CASHIER_USER.fullName, exact: true }).click()
  await expect(page.locator('main h1').first()).toContainText(NIGHT_CASHIER_USER.fullName)
  // STEP 7: Atur kata sandi baru, and its message at the top of the page
  const card = setupCard(page, tx('users.newPasswordTitle'))
  await card.getByLabel(tx('users.newPassword'), { exact: true }).fill(NIGHT_CASHIER_USER.reset)
  await card.getByRole('button', { name: tx('users.setPassword'), exact: true }).click()
  const notice = page.locator('main [role=status]').filter({ hasText: tx('users.passwordSet') })
  await expect(notice).toBeVisible()
  await page.evaluate(() => window.scrollTo(0, 0))
  await shotAround([notice, card], 'pengaturan-pengguna-07')
}

/** 13-pengaturan/tipe-kamar-dan-kamar.md: the forms are filled and left unsaved (a new room type or room would show in every later grid and list). */
export async function flowTipeKamar(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1: Pengaturan → Tipe kamar
  await loginAs('admin', '/setup/room-types')
  await expect(page.getByTestId('type-STD')).toBeVisible()
  await shotAround([page.locator('main h1').first(), page.locator('main table').first()], 'pengaturan-tipekamar-01')
  // STEP 2: Tipe kamar baru
  await page.getByTestId('new-type').click()
  const type = page.locator('main form').first()
  await type.locator('input[name=code]').fill('FAM')
  await type.locator('input[name=name]').fill('Family Room')
  await type.locator('input[name=description]').fill('Dua kamar tidur terhubung')
  await type.locator('input[name=max_adult]').fill('4')
  await type.locator('input[name=max_child]').fill('2')
  await type.locator('input[name=sort_order]').fill('50')
  await type.locator('input[name=max_occupancy]').fill('5')
  await type.locator('input[name=base_occupancy]').fill('2')
  await shot(type, 'pengaturan-tipekamar-02')
  await type.getByRole('button', { name: tx('common.cancel'), exact: true }).click()
  // STEP 5-6: Pengaturan → Kamar → Kamar baru
  await page.goto('/setup/rooms')
  await page.getByTestId('new-room').click()
  const room = page.locator('main form').first()
  await room.locator('input[name=room_number]').fill('601')
  await selectContaining(room.locator('select[name=room_type_id]'), 'DLX')
  await room.locator('select[name=initial_housekeeping_status]').selectOption('DIRTY')
  await selectContaining(room.locator('select[name=bed_type_id]'), 'King')
  await room.locator('input[name=floor]').fill('6')
  await room.locator('input[name=building]').fill('Gedung utama')
  await shot(room, 'pengaturan-kamar-06')
  await room.getByRole('button', { name: tx('common.cancel'), exact: true }).click()
}

/** 13-pengaturan/pajak-dan-kode-biaya.md: a new charge code SPA (service, exclusive) with SC then PB1, and its calculator. */
export async function flowPajakKodeBiaya(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1: Pengaturan → Pajak & biaya layanan (PB1 and SC of the pictures' hotel, world.ts)
  await loginAs('admin', '/setup/taxes')
  await expect(page.getByTestId('section-tax').getByTestId('tax-PB1')).toBeVisible()
  await expect(page.getByTestId('section-service').getByTestId('service-SC')).toBeVisible()
  await page.evaluate(() => window.scrollTo(0, 0))
  await shotAround([page.locator('main h1').first(), page.getByTestId('section-service')], 'pengaturan-pajak-01')
  // STEP 5: Kode biaya → Kode biaya baru
  await page.goto('/setup/charge-codes')
  await expect(page.getByTestId('code-ROOM')).toBeVisible()
  const fresh = (await page.getByTestId('code-SPA').count()) === 0
  if (fresh) {
    await page.getByTestId('new-code').click()
    const create = page.getByTestId('code-form')
    await create.locator('input[name=code]').fill('SPA')
    await create.locator('input[name=name]').fill('Spa dan pijat')
    await create.locator('select[name=charge_type]').selectOption('SERVICE')
    await create.locator('select[name=price_mode]').selectOption('EXCLUSIVE')
    await create.locator('input[name=default_unit_price]').fill('350000')
    await create.locator('input[name=gl_account_code]').fill('4310')
    await selectContaining(create.getByTestId('department-select'), 'OOD')
    await shot(create, 'pengaturan-kodebiaya-05')
    // STEP 6: Simpan: the code is in the list, without rules yet
    await create.getByRole('button', { name: tx('common.save'), exact: true }).click()
    await expect(page.getByTestId('code-SPA')).toBeVisible()
  }
  // STEP 7: Ubah → Aturan: the service charge, then PB1, Simpan aturan
  await page.getByTestId('code-SPA').getByRole('button', { name: tx('common.edit'), exact: true }).click()
  const form = page.getByTestId('code-form')
  await expect(form.getByTestId('rules')).toBeVisible()
  if (fresh) {
    await selectContaining(form.locator('select[name=add_service]'), 'SC ·')
    await form.getByTestId('add-service').click()
    await selectContaining(form.locator('select[name=add_tax]'), 'PB1 ·')
    await form.getByTestId('add-tax').click()
    await form.getByTestId('save-rules').click()
    await expect(form.getByTestId('rules-saved')).toBeVisible()
  }
  // STEP 8: Kalkulator: one treatment, nothing is posted
  const calc = form.getByTestId('calculator')
  await calc.locator('input[name=quantity]').fill('1')
  await calc.locator('input[name=unit_price]').fill('350000')
  await calc.getByTestId('calculate').click()
  await expect(calc.getByTestId('result')).toBeVisible()
  await expect(calc.getByTestId('tax-line')).toHaveCount(1)
  await shot(calc, 'pengaturan-kodebiaya-08')
  await form.getByRole('button', { name: tx('common.close'), exact: true }).click()
}

/** 13-pengaturan/paket-tarif-dan-grid-tarif.md: the rate plan BB (room and breakfast) and two weeks of its STD price. */
export async function flowPaketTarif(page: Page, loginAs: LoginAs, data: Factories): Promise<void> {
  const bd = await data.businessDate()
  // STEP 1-2: Pengaturan → Paket tarif → Paket tarif baru
  await loginAs('admin', '/setup/rate-plans')
  await expect(page.getByTestId('plan-RO')).toBeVisible()
  if (!(await page.getByTestId('plan-BB').count())) {
    await page.getByTestId('new-plan').click()
    const form = page.getByTestId('plan-form')
    await form.locator('input[name=code]').fill('BB')
    await form.locator('input[name=name]').fill('Kamar dan sarapan')
    await form.locator('select[name=meal_plan]').selectOption('BB')
    await form.locator('select[name=occupancy_kind]').selectOption('PAID')
    await selectContaining(form.locator('select[name=room_charge_code_id]'), 'ROOM ·')
    await expect(form.getByTestId('mode-hint')).toBeVisible()
    await form.locator('input[name=description]').fill('Termasuk sarapan untuk dua orang')
    await form.locator('input[name=cancellation_policy]').fill('Gratis dibatalkan sampai H-2')
    await shot(form, 'pengaturan-pakettarif-02')
    // STEP 3: Simpan
    await form.getByRole('button', { name: tx('common.save'), exact: true }).click()
    await expect(page.getByTestId('plan-BB')).toBeVisible()
  }
  // STEP 5: Pengaturan → Grid tarif, the plan BB
  await page.goto('/setup/rates')
  await expect(page.getByTestId('grid')).toBeVisible()
  await selectContaining(page.locator('select[name=rate_plan]'), 'BB ·')
  const first = page.getByTestId(`cell-STD-${bd}`)
  await expect(first).toBeVisible()
  // STEP 6: a click on the first night of STD fills Atur tarif; fourteen nights from it
  await first.click()
  const fill = page.getByTestId('fill-form')
  await setDate(fill, 'to', addDays(bd, 14))
  await fill.locator('input[name=amount]').fill('850000')
  await shot(fill, 'pengaturan-gridtarif-06')
  // STEP 7: Terapkan: the message, and the prices in the grid
  await fill.getByRole('button', { name: tx('rateGrid.apply'), exact: true }).click()
  await expect(fill.getByTestId('notice')).toBeVisible()
  await expect(first).not.toHaveText('—')
  await page.evaluate(() => window.scrollTo(0, 0))
  await shotAround([page.locator('main h1').first(), page.getByTestId('grid')], 'pengaturan-gridtarif-07')
}

/** 13-pengaturan/pembatasan-yield-dan-kuota.md: a minimum stay on Saturdays for BB only (no other picture books BB), a yield rule that only
 *  starts at 90% occupancy (the pictures' hotel never gets there), and the price check. */
export async function flowPembatasanYield(page: Page, loginAs: LoginAs, data: Factories): Promise<void> {
  const bd = await data.businessDate()
  // STEP 1: Pengaturan → Pembatasan penjualan, the plan BB (flowPaketTarif made it; without it the grid of RO is shown unchanged)
  await loginAs('admin', '/setup/restrictions')
  await expect(page.getByTestId('grid')).toBeVisible()
  const plan = page.locator('select[name=rate_plan]')
  if (await plan.locator('option', { hasText: 'BB ·' }).count()) {
    await selectContaining(plan, 'BB ·')
    // STEP 2: Atur pembatasan: arrivals on a Saturday of the next two weeks stay two nights at least
    const form = page.getByTestId('fill-form')
    await setDate(form, 'from', bd)
    await setDate(form, 'to', addDays(bd, 14))
    await form.locator('input[name="plan-BB"]').check()
    await form.locator('input[name="weekday-SAT"]').check()
    await form.locator('input[name=min_stay]').fill('2')
    await form.locator('input[name=note]').fill('Akhir pekan minimal 2 malam')
    // STEP 3: Terapkan: the grid shows ≥2 on the Saturdays
    await form.getByTestId('apply').click()
    await expect(form.getByTestId('notice')).toBeVisible()
    await expect(page.getByTestId('grid').getByTestId('mark-min_stay').first()).toBeVisible()
  }
  await page.evaluate(() => window.scrollTo(0, 0))
  await shotAround([page.getByTestId('grid'), page.getByTestId('legend')], 'pengaturan-pembatasan-03')
  // STEP 4-5: Pengaturan → Aturan yield → Aturan baru
  await page.goto('/setup/yield-rules')
  await expect(page.getByTestId('new-rule')).toBeVisible()
  if (!(await page.getByTestId('rule-PENUH90').count())) {
    await page.getByTestId('new-rule').click()
    const form = page.getByTestId('rule-form')
    await form.locator('input[name=code]').fill('PENUH90')
    await form.locator('input[name=name]').fill('Hotel hampir penuh')
    await form.locator('input[name=priority]').fill('100')
    await form.locator('input[name=occupancy_from]').fill('90')
    await form.locator('input[name=occupancy_to]').fill('100')
    await form.locator('select[name=adjustment_type]').selectOption('PERCENT')
    await form.locator('input[name=adjustment_value]').fill('15')
    await form.locator('input[name=cap_amount]').fill('2500000')
    await shot(form, 'pengaturan-yield-05')
    // STEP 6: Simpan
    await form.getByRole('button', { name: tx('common.save'), exact: true }).click()
    await expect(page.getByTestId('rule-PENUH90')).toBeVisible()
  }
  // STEP 7: Cek harga: two nights of STD on RO
  const quote = page.getByTestId('quote-form')
  await selectContaining(quote.locator('select[name=quote_plan]'), 'RO ·')
  await selectContaining(quote.locator('select[name=quote_type]'), 'STD ·')
  await setDate(quote, 'quote_arrival', addDays(bd, 3))
  await setDate(quote, 'quote_departure', addDays(bd, 5))
  await quote.getByTestId('quote-run').click()
  await expect(quote.getByTestId('quote')).toBeVisible()
  await shot(quote, 'pengaturan-yield-07')
}

/** 13-pengaturan/aturan-persetujuan.md: the rule of one kind is changed on the screen and not saved (the other pictures keep the hotel's rules). */
export async function flowAturanPersetujuan(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1: Pengaturan → Aturan persetujuan
  await loginAs('admin', '/setup/approval-rules')
  const rules = page.getByTestId('rules')
  await expect(rules.getByTestId('rule-FOLIO_ADJUSTMENT')).toBeVisible()
  await shotAround([page.locator('main h1').first(), rules.getByTestId('rule-FOLIO_TRANSFER')], 'pengaturan-persetujuan-01')
  // STEP 2: the asset disposals: Selalu orang lain since the demo data (pms-seed assets), with the warning about its approvers
  await shot(rules.getByTestId('rule-ASSET_DISPOSAL'), 'pengaturan-persetujuan-02')
  // STEP 3: Penyesuaian folio: Orang lain di atas batas, Rp500.000
  const row = rules.getByTestId('rule-FOLIO_ADJUSTMENT')
  await row.locator('select[name=rule_FOLIO_ADJUSTMENT]').selectOption('DISTINCT_ABOVE')
  await row.locator('input[name=limit_FOLIO_ADJUSTMENT]').fill('500000')
  await expect(row).toHaveAttribute('data-changed', 'true')
  await expect(page.getByTestId('save')).toBeEnabled()
  await shot(row, 'pengaturan-persetujuan-03')
  // not saved: a reload shows the rules as they are
  await page.reload()
  const again = page.getByTestId('rules').getByTestId('rule-FOLIO_ADJUSTMENT')
  await expect(again).toBeVisible()
  await expect(page.getByTestId('save')).toBeDisabled()
}

/** 13-pengaturan/langganan-dan-sistem.md */
export async function flowLangganan(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1: Pengaturan → Langganan (the E2E tenant is on the default plan, in its trial)
  await loginAs('admin', '/setup/subscription')
  await expect(page.getByTestId('subscription-plan')).toBeVisible()
  await expect(page.getByTestId('subscription-invoices')).toBeVisible()
  await shotAround([page.locator('main h1').first(), page.getByTestId('subscription-invoices')], 'pengaturan-langganan-01')
  // STEP 6: Pengaturan → Sistem → Muat ulang
  await page.goto('/setup/system')
  await page.getByTestId('system-refresh').click()
  await expect(page.getByTestId('system-status-card')).toContainText(tx('system.lastChecked', { time: '' }).trim())
  await shotAround([page.locator('main h1').first(), page.getByTestId('system-status-card')], 'pengaturan-sistem-06')
}

/** 14-admin-platform/console-platform.md: only the sign-in page (P1). The E2E world has no platform operator, so the other steps of the page
 *  have no pictures yet (they wait for an operator made with pms-admin create-operator and an authenticator enrolment). */
export async function flowConsolePlatform(page: Page): Promise<void> {
  // STEP 1: the sign-in page of the console, in Indonesian (the language of the browser is the one the hotel screens keep)
  await page.context().addInitScript(() => {
    try { localStorage.setItem('pms.locale', 'id') } catch { /* storage refused: the page shows its default language */ }
  })
  await page.goto('/platform/login')
  const step = page.getByTestId('step-password')
  await expect(step).toBeVisible()
  await expect(step.getByRole('heading', { name: tx('platform.login.title') })).toBeVisible()
  await step.locator('input[name=email]').fill('operator@kamarapms.example')
  await shot(page.locator('main > div').first(), 'platform-login-01', { pad: 24 })
}


// =====================================================================================================================
// The screens the route coverage of 2026-11-26 found without a page of their own: 03 Kasir (the list of payments), 04 Pelayan (the waiter's phone),
// 05 Laporan banquet, 10 Pengaturan aset. They use the helpers above (chooseOutlet, RESTO, onlyMethod, BQ_EVENT, selectContaining, closeToasts, tallWindow).
// Where they go in docs-screenshots.spec.ts: "03 daftar kasir" after "03 membuka folio untuk outlet" (it shows the QRIS payment that "03 void dan refund"
// voids); "04 pelayan" after "04 laporan outlet"; "05 laporan banquet" after "05 membatalkan acara" (the event of "05 membuat acara dan sesi" and the
// cancellation of "05 membatalkan acara"); "10 pengaturan aset" after "10 laporan aset".

/** 03-kasir-dan-pembayaran/daftar-kasir.md */
export async function flowDaftarKasir(page: Page, loginAs: LoginAs): Promise<void> {
  // STEP 1-2: Tagihan → Kasir (the cashier Kevin): the payments of the business date, the totals per method
  await loginAs('cashier', '/cashier')
  await expect(page.getByTestId('totals')).toBeVisible()
  const rows = page.locator('[data-testid^="payment-"]:visible')
  await expect(rows.first()).toBeVisible()
  await shotPage(page, 'kasir-daftar-01')
  // STEP 3: Metode QRIS → Tampilkan: the QRIS payment taken by mistake in "03 void dan refund", struck through
  await page.locator('select[name=method]').selectOption('QRIS')
  await page.getByRole('button', { name: tx('cashier.show'), exact: true }).click()
  await expect(rows.first()).toContainText(tx('cashier.QRIS'))
  await expect(rows.filter({ hasText: tx('status.VOIDED') }).first()).toBeVisible()
  await closeToasts(page)
  await shotAround([page.locator('[data-slot=filter-bar]:visible').first(), page.locator('main table').first()], 'kasir-daftar-03')
  // STEP 4: the columns of a row (the order by a heading is not clicked: the picture of step 3 shows the list newest first)
  await expect(rows.first()).toContainText(tx('cashier.PAYMENT'))
  // STEP 5: Kuitansi (the PDF opens in a new tab: the picture run only checks the button)
  await expect(rows.first().locator('[data-testid^="receipt-"]')).toBeVisible()
  // STEP 6: the folio of the payment, from the column Folio
  await rows.first().getByRole('link').first().click()
  await expect(page).toHaveURL(/\/folios\/\d+/)
}

/** The waiter's phone (the shell answers the width: no sidebar, the cards of the waiter screen). */
const PHONE = { width: 390, height: 844 }

/** 04-pos/pelayan.md */
export async function flowPelayan(page: Page, loginAs: LoginAs): Promise<void> {
  const desk = page.viewportSize() ?? { width: 1440, height: 900 }
  let billId = ''
  // the waiter Putu signs in, then the window becomes a phone
  await loginAs('pos', '/pos/waiter')
  await page.setViewportSize(PHONE)
  try {
    // STEP 1: POS → Pelayan, the restaurant: the tables of Dalam ruangan
    await expect(page.getByTestId('waiter-floor')).toBeVisible()
    const outlets = page.getByTestId('outlet-select')
    await expect(outlets).toBeVisible()
    await chooseOutlet(outlets, RESTO)
    const table = page.getByTestId('waiter-tables').getByTestId('table-7')
    await expect(table).toHaveAttribute('data-status', 'FREE')
    await closeToasts(page)
    await shotPage(page, 'pos-pelayan-01')
    // STEP 2: table 7 → three guests → Buka bill
    await table.click()
    const guests = page.getByTestId('guests-dialog')
    await expect(guests.getByTestId('guests-count')).toHaveText('2')
    await guests.getByTestId('guests-more').click()
    await expect(guests.getByTestId('guests-count')).toHaveText('3')
    await guests.getByTestId('guests-open').click()
    await expect(page).toHaveURL(/\/pos\/waiter\/bills\/\d+$/)
    billId = page.url().match(/\/bills\/(\d+)$/)?.[1] ?? ''
    // STEP 3: the order screen: the table and the guests in the title, the categories under Cari menu
    const order = page.getByTestId('waiter-order')
    await expect(page.getByTestId('waiter-title')).toContainText('7')
    const menu = page.getByTestId('waiter-menu')
    // STEP 4: a fried rice with its choices (Makanan utama), then two iced teas (Tambah, +) and a Bali coffee (Minuman)
    await order.getByRole('tab', { name: 'Makanan utama', exact: true }).click()
    await menu.locator('li').filter({ hasText: 'Nasi Goreng Kampung' }).locator('[data-testid^="add-"]').click()
    const options = page.getByTestId('options-dialog')
    await options.getByTestId('options-note').fill('tanpa bawang')
    await options.getByTestId('options-add').click()
    await expect(options).toHaveCount(0)
    await order.getByRole('tab', { name: 'Minuman', exact: true }).click()
    const tea = menu.locator('li').filter({ hasText: 'Es Teh Manis' })
    await tea.locator('[data-testid^="add-"]').click()
    await expect(tea.getByTestId('stepper')).toBeVisible()
    await tea.locator('[data-testid^="more-"]').click()
    await expect(tea.locator('[data-testid^="qty-"]')).toHaveText('2')
    await menu.locator('li').filter({ hasText: 'Kopi Bali' }).locator('[data-testid^="add-"]').click()
    await expect(page.getByTestId('draft-summary')).toContainText(tx('posWaiter.draftSummary', { n: 4, total: '' }).split(' · ')[0]!)
    await shotPage(page, 'pos-pelayan-04')
    // STEP 5: Lihat bill (read only), then back
    await page.getByTestId('view-bill').click()
    const bill = page.getByTestId('bill-dialog')
    await expect(bill).toContainText('Es Teh Manis')
    await expect(bill).toContainText(tx('posBill.drafts'))
    await page.keyboard.press('Escape')
    await expect(bill).toHaveCount(0)
    // STEP 6: Kirim ke dapur dan bar: "sudah dipesan" on the menu
    await page.getByTestId('waiter-send').click()
    await expect(page.getByTestId('waiter-notice')).toHaveText(tx('posBill.sent'))
    await expect(tea.getByTestId('ordered')).toBeVisible()
    await shotPage(page, 'pos-pelayan-06')
    // STEP 7: ‹ back to the tables: table 7 is taken
    await page.getByTestId('waiter-back').click()
    await expect(page.getByTestId('waiter-tables').getByTestId('table-7')).toHaveAttribute('data-status', 'OCCUPIED')
  } finally {
    await page.setViewportSize(desk)
  }
  // (not a step: the waiter takes no money) the cashier of the outlet takes a card for table 7, so no open bill is left for the night audit
  if (!billId) return
  await page.goto(`/pos/bills/${billId}`)
  await page.getByTestId('pay').click()
  const pay = page.getByTestId('pay-dialog')
  await onlyMethod(pay, 'CARD')
  await pay.getByTestId('exact-CARD').click()
  await pay.getByTestId('pay-submit').click()
  await expect(page.getByTestId('bill-closed')).toBeVisible()
}

/** 05-banquet/laporan-banquet.md */
export async function flowLaporanBanquet(page: Page, loginAs: LoginAs, w: DocsWorld): Promise<void> {
  // STEP 1: Banquet → Laporan banquet: one card per report
  await loginAs('admin', '/banquet/reports')
  await expect(page.getByTestId('report-card-schedule')).toBeVisible()
  await shotPage(page, 'banquet-laporan-01')
  // STEP 5: Jadwal harian of the business date: the first day of the meeting of "05 membuat acara dan sesi"
  await page.getByTestId('report-card-schedule').click()
  await expect(page).toHaveURL(/\/banquet\/reports\/schedule$/)
  await setDate(page.getByTestId('report-period'), 'day', w.businessDate)
  await page.getByTestId('report-show').click()
  const schedule = page.getByTestId('schedule-table')
  await expect(schedule).toContainText(BQ_EVENT)
  await shotAround([page.locator('main h1').first(), schedule], 'banquet-laporan-05')
  // STEP 6: Cetak (the print dialog of the browser: the picture run only checks the button)
  await expect(page.getByTestId('report-print')).toBeVisible()
  // STEP 7: Perkiraan pendapatan, six months from the first of this month, with service and tax
  await page.goto('/banquet/reports/forecast')
  await expect(page.getByTestId('forecast-kpi')).toBeVisible()
  await page.getByTestId('forecast-tax').check()
  await expect(page.getByTestId('forecast-months')).toBeVisible()
  await closeToasts(page)
  await shotAround([page.locator('main h1').first(), page.getByTestId('forecast-months')], 'banquet-laporan-07')
  // STEP 8: the other reports open on the month of the business date (a table, or "Tidak ada data pada periode ini.")
  for (const kind of ['rooms', 'guests', 'sales', 'lost'] as const) {
    await page.goto('/banquet/reports')
    await page.getByTestId(`report-card-${kind}`).click()
    await expect(page.getByTestId('report-period')).toBeVisible()
    await expect(page.locator(`[data-testid=${kind}-table], [data-testid=${kind}-empty]`).first()).toBeVisible()
  }
  // the cancelled seminar of "05 membatalkan acara" is in the report of lost and cancelled events
  await expect(page.getByTestId('lost-table')).toContainText('Seminar Keuangan Daerah')
}

/** The account of the example category of pengaturan-aset (a new asset account that no category uses): made once, through the API. */
async function kitchenAccount(data: Factories): Promise<boolean> {
  const find = async (code: string) => (await data.api.get<{ data: { id: number; code: string }[] }>(`${data.P}/accounting/accounts`, { q: code })).data.find((a) => a.code === code)
  if (await find('1580')) return true
  const parent = await find('1500')
  const made = await data.api.soft('POST', `${data.P}/accounting/accounts`, { code: '1580', name: 'Peralatan dapur', account_type: 'ASSET', parent_id: parent?.id ?? null, statement_group: 'FIXED_ASSETS' })
  if (!made) console.warn('[docs] the account 1580 Peralatan dapur could not be made: the picture of the new category keeps Akun harga empty')
  return made !== null
}

/** 10-aset-tetap/pengaturan-aset.md */
export async function flowPengaturanAset(page: Page, loginAs: LoginAs, data: Factories): Promise<void> {
  const account = await kitchenAccount(data)
  // STEP 1: Aset tetap → Pengaturan aset: the categories of the hotel
  await loginAs('admin', '/assets/settings')
  await expect(page.getByTestId('categories-table')).toBeVisible()
  await expect(page.getByTestId('category-FFE')).toBeVisible()
  // STEP 2: Kategori baru
  await page.getByTestId('category-new').click()
  const dlg = page.getByTestId('category-dialog')
  // STEP 3: kitchen equipment, five years straight line (filled and cancelled: the later pages keep the seven categories of the hotel)
  await dlg.locator('input[name=code]').fill('DAPUR')
  await dlg.locator('input[name=name]').fill('Peralatan dapur')
  await dlg.locator('select[name=kind]').selectOption('FFE')
  if (account) {
    await expect(dlg.locator('select[name=cost_account_id] option', { hasText: '1580' }).first()).toBeAttached()
    await selectContaining(dlg.locator('select[name=cost_account_id]'), '1580')
  }
  await selectContaining(dlg.locator('select[name=accumulated_account_id]'), '1592')
  await selectContaining(dlg.locator('select[name=expense_account_id]'), '7520')
  await dlg.locator('select[name=method]').selectOption('SL')
  await dlg.locator('input[name=life_months]').fill('60')
  await dlg.locator('select[name=fiscal_group]').selectOption('G2')
  await dlg.locator('select[name=fiscal_method]').selectOption('SL')
  await tallWindow(page, 1200, () => shot(dlg, 'aset-pengaturan-03', { pad: 0 }))
  // STEP 4: Simpan is the step of the page; the picture run cancels
  await dlg.getByRole('button', { name: tx('common.cancel'), exact: true }).click()
  await expect(dlg).toHaveCount(0)
  // STEP 5: Pengaturan properti: the capitalisation threshold of the example (typed, not saved)
  const settings = page.getByTestId('settings-card')
  await settings.locator('input[name=capitalization_threshold]').fill('2500000')
  await shot(settings, 'aset-pengaturan-05')
  // STEP 6: Simpan is not pressed: the page is read again, so the typed threshold goes
  await page.reload()
  const locations = page.getByTestId('locations-card')
  await expect(locations.getByTestId('location-LOBI')).toBeVisible()
  // STEP 7: Area bebas baru: the pool area (a new location, made once)
  if (!(await page.getByTestId('location-KOLAM').count())) {
    await page.getByTestId('location-new').click()
    const ldlg = page.getByTestId('location-dialog')
    await ldlg.locator('input[name=code]').fill('KOLAM')
    await ldlg.locator('input[name=name]').fill('Area kolam renang')
    await ldlg.getByTestId('location-save').click()
    await expect(ldlg).toHaveCount(0)
  }
  await expect(page.getByTestId('location-KOLAM')).toBeVisible()
  // STEP 8: Buat lokasi dari → Ruang pertemuan → Yang mana: Saraswati (chosen, not made)
  const ensure = page.getByTestId('ensure-form')
  await ensure.locator('select[name=kind]').selectOption('BANQUET_ROOM')
  await expect(ensure.locator('select[name=ref_id] option', { hasText: 'Saraswati' }).first()).toBeAttached()
  await selectContaining(ensure.locator('select[name=ref_id]'), 'Saraswati')
  await closeToasts(page)
  await shot(locations, 'aset-pengaturan-08')
  // STEP 9: Pemeriksaan register
  await expect(page.getByTestId('check-card')).toBeVisible()
  // KD-23 (not a step): Matikan on the new pool area. The page sends the switch without the name and the server wants a name, so the location is
  // expected to stay on with "Ada isian yang tidak valid." over the page. The outcome goes to the run log ([KD-23] lines) and never fails the test.
  await page.getByTestId('location-toggle-KOLAM').click()
  await page.waitForLoadState('networkidle', { timeout: 3_000 }).catch(() => undefined)
  const banner = page.getByTestId('form-error')
  const said = (await banner.count()) ? (await banner.first().innerText()).replace(/\s+/g, ' ').trim().slice(0, 200) : '(no banner)'
  const row = (await page.getByTestId('location-KOLAM').innerText()).replace(/\s+/g, ' ').trim()
  console.log(`[KD-23] after Matikan on KOLAM: row "${row}" · banner "${said}"`)
}
