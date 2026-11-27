import { test, expect } from '../support/fixtures'
import { addDays, uid, type CheckedIn, type Factories } from '../support/factories'
import { tx } from '../support/i18n'

/**
 * The night audit closes the business day, so it runs last (the project "final", the teardown of setup) and alone.
 * What the module tests left behind is real work for it: arrivals that never checked in become no-shows through the screen,
 * the guests in house get their room night, and the business date moves on by one day.
 * E2E_SKIP_NIGHT_AUDIT=1 leaves the day open (to run one spec again and again on the same day).
 */
test.describe.configure({ mode: 'serial' })

test.describe('Night audit', () => {
  test.skip(!!process.env.E2E_SKIP_NIGHT_AUDIT, 'E2E_SKIP_NIGHT_AUDIT is set')

  // NA-04: a guest checked in before the audit and booked past the new business date, who then leaves early in the daytime
  let earlyLeaver: CheckedIn | undefined
  // NA-05: a company with two invoices issued today on terms of 0 days, so they are overdue once the audit has closed the day
  let debtor: { id: number; code: string; invoices: string[] } | undefined
  // NA-06: two card payments of today, waiting for their settlement once the audit has closed the day
  let cards: { day: string; payments: string[] } | undefined
  // NA-07: a company that paid both its invoices of today with one receipt recorded on account (not allocated)
  let advance: { id: number; code: string; invoices: string[]; receipt: string } | undefined

  /** Two card payments (300,000 and 500,000) of a guest in house: their journal lines wait for the acquirer once the day is closed. */
  async function cardPayments(data: Factories): Promise<{ day: string; payments: string[] }> {
    const ih = await data.inHouse({ nights: 2 })
    await data.postCharge(ih.folio.id, 'FOOD_BEVERAGE', '800000', 'Jamuan dibayar kartu (E2E)')
    const a = await data.pay(ih.folio.id, '300000', 'CARD')
    const b = await data.pay(ih.folio.id, '500000', 'CARD')
    return { day: await data.businessDate(), payments: [a.payment.payment_number, b.payment.payment_number] }
  }

  /** A company that owes two invoices of today (terms 0): each holds the transfer of a guest who checked out. */
  async function companyWithTwoInvoices(data: Factories): Promise<{ id: number; code: string; invoices: string[] }> {
    const u = uid()
    const company = await data.api.post<{ id: number; code: string }>(`${data.P}/companies`, {
      code: `OD${u}`.slice(0, 10), name: `PT Telat Bayar ${u}`, contact_name: 'Ibu Uji', email: `ar.${u.toLowerCase()}@e2e.example`, credit_limit: '50000000', payment_terms_days: 0,
    })
    const invoices: string[] = []
    for (const amount of ['200000', '300000']) {
      const ih = await data.inHouse({ nights: 1, companyId: company.id })
      await data.postCharge(ih.folio.id, 'FOOD_BEVERAGE', amount, 'Jamuan perusahaan (E2E)')
      const balance = (await data.folio(ih.folio.id)).balance
      await data.api.post(`${data.P}/folios/${ih.folio.id}/city-ledger-transfers`, { company_id: company.id, amount: balance, reference_number: `PO-${uid()}` })
      // the check-out posts tonight's room charge: the preview says what to pay first
      const pv = await data.api.get<{ folios: { folio_id: number; due: string }[] }>(`${data.P}/stays/${ih.stay.id}/check-out/preview`)
      const due = pv.folios.find((f) => f.folio_id === ih.folio.id)?.due ?? '0'
      if (Number(due) > 0) await data.pay(ih.folio.id, due)
      const st = await data.stay(ih.stay.id)
      await data.api.post(`${data.P}/stays/${ih.stay.id}/check-out`, { version: st.stay.version, confirm_early_departure: false })
      const candidates = (await data.api.get<{ data: { payment_id: number; invoiceable: boolean }[] }>(`${data.P}/city-ledger/accounts/${company.id}/invoice-candidates`)).data
      const inv = await data.api.post<{ invoice_number: string }>(`${data.P}/city-ledger/accounts/${company.id}/invoices`, { payment_ids: candidates.filter((c) => c.invoiceable).map((c) => c.payment_id) })
      invoices.push(inv.invoice_number)
    }
    return { ...company, invoices }
  }

  test('NA-01 menandai no-show, menjalankan night audit, dan tanggal bisnis maju satu hari', async ({ page, loginAs, data }) => {
    test.setTimeout(180_000)
    const bd = await data.businessDate()
    const ih = await data.inHouse({ nights: 2 }) // a guest in house tonight: the audit posts the night
    const noShow = await data.reservation({ nights: 1 }) // an arrival that never comes
    earlyLeaver = await data.inHouse({ nights: 3 }) // NA-04: in house over the audit, booked two nights past it
    debtor = await companyWithTwoInvoices(data) // NA-05: due today, overdue after the audit
    cards = await cardPayments(data) // NA-06: settled from the bank after the audit
    // NA-07: the money of the company is recorded today without allocating it, so its invoices look unpaid and overdue after the audit
    const paid = await companyWithTwoInvoices(data)
    const owes = await data.api.get<{ balance: string }>(`${data.P}/city-ledger/accounts/${paid.id}`)
    const onAccount = await data.api.post<{ receipt: { receipt_number: string; unallocated: string } }>(`${data.P}/city-ledger/accounts/${paid.id}/receipts`, {
      amount: owes.balance, payment_method: 'BANK_TRANSFER', reference_number: `TRF-${uid()}`,
    })
    expect(onAccount.receipt.unallocated).toBe(owes.balance)
    advance = { ...paid, receipt: onAccount.receipt.receipt_number }

    // an open count freezes its warehouse and holds the close of the day: setup runs pms-seed inventory with -no-open-count, and no module leaves one
    const open = (await data.api.get<{ data: { cnt_number: string }[] }>(`${data.P}/inventory/counts`, { status: 'OPEN' })).data
    expect(open, 'open counts that would hold the night audit').toEqual([])

    await loginAs('admin', '/night-audit')
    await expect(page.getByTestId('audit-date')).toBeVisible()

    // arrivals of the day that did not check in: mark them all no-show
    const arrivals = page.getByTestId('arrivals')
    await expect(arrivals.getByTestId(`arrival-${noShow.rooms[0]!.id}`)).toBeVisible()
    await arrivals.getByTestId('select-all').check()
    await page.getByTestId('noshow-form').locator('input[name=reason]').fill('Tidak datang (E2E)')
    await page.getByTestId('noshow-form').locator('input[name=confirm]').check()
    await page.getByTestId('mark-no-shows').click()
    await expect(page.getByTestId('notice')).toBeVisible()
    await expect(page.getByTestId('no-arrivals')).toBeVisible() // the preview is read again
    const line = await data.api.get<{ rooms: { status: string }[] }>(`${data.P}/reservations/${noShow.id}`)
    expect(line.rooms[0]?.status).toBe('NO_SHOW')

    // nothing else may hold the day: if something does, the reasons of the server are the failure message
    const preview = await data.api.get<{ can_run: boolean; blockers: unknown }>(`${data.P}/night-audit/preview`).catch(() => null)
    if (preview && !preview.can_run) throw new Error(`the night audit cannot run: ${JSON.stringify(preview.blockers).slice(0, 1500)}`)
    await expect(page.getByTestId('cannot-run')).toHaveCount(0)

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

  test('NA-03 transaksi setelah night audit tercatat di tanggal bisnis baru, bukan hari yang sudah ditutup', async ({ page, loginAs, data }) => {
    const bd = await data.businessDate()
    const ih = await data.inHouse({ nights: 1 })
    const { item } = await data.postCharge(ih.folio.id, 'FOOD_BEVERAGE', '10000', 'Kopi pagi (E2E)')
    const folio = await data.api.get<{ items: { id: number; business_date: string }[] }>(`${data.P}/folios/${ih.folio.id}`)
    expect(folio.items.find((i) => i.id === item.id)?.business_date).toBe(bd)
    await loginAs('admin', '/')
    await expect(page.getByTestId('business-date')).toBeVisible()
  })

  // KD-06 (docs/user-guide/_laporan.md): the check-out wizard asked an early leaver who arrived before today for tonight's room charge,
  // which the check-out never posts in the daytime (the stay is cut to today), so the button stayed disabled. The wizard now asks
  // for what the check-out preview of the server says: the balance of the folio, nothing for tonight.
  test('NA-04 tamu yang pulang lebih awal setelah audit malam bisa check-out dengan membayar saldo folio saja', async ({ page, loginAs, data }) => {
    expect(earlyLeaver, 'NA-01 checked the guest in').toBeDefined()
    const guest = earlyLeaver!
    const bd = await data.businessDate()
    const before = await data.stay(guest.stay.id)
    expect(before.stay.departure_date > bd, 'booked past today').toBe(true)
    const folio = await data.folio(guest.folio.id)
    expect(Number(folio.balance)).toBeGreaterThan(0) // the night the audit posted
    await data.pay(guest.folio.id, folio.balance) // what the desk takes: the balance of the folio page

    await loginAs('admin', `/stays/${guest.stay.id}`)
    await page.getByTestId('checkout').click()
    const wizard = page.getByTestId('checkout-wizard')
    await expect(wizard.getByTestId('step-departure')).toHaveAttribute('data-settled', 'true') // the server's preview has answered
    await wizard.locator('input[name=confirm_early]').check()
    await expect(wizard.getByTestId('checkout-due-reason')).toHaveCount(0) // nothing more is asked for
    await expect(wizard.getByTestId('checkout-submit')).toBeEnabled()
    const answer = page.waitForResponse((r) => r.url().endsWith(`/stays/${guest.stay.id}/check-out`) && r.request().method() === 'POST')
    await wizard.getByTestId('checkout-submit').click()
    const r = await answer
    expect(r.status(), `the check-out was refused: ${await r.text()}`).toBe(200)
    // the stay page reads the stay again and closes the wizard (its done step may show only for a moment), see FD-08
    await expect(page.getByTestId('notice')).toContainText(tx('stay.checkedOut', { n: 0 }))
    await expect(wizard).toHaveCount(0)

    const after = await data.stay(guest.stay.id)
    expect(after.stay.status).toBe('CHECKED_OUT')
    expect(after.stay.departure_date).toBe(bd)
    const items = (await data.api.get<{ items: { transaction_type: string; service_date: string }[] }>(`${data.P}/folios/${guest.folio.id}`)).items
    expect(items.some((i) => i.transaction_type === 'CHARGE' && i.service_date === bd), 'tonight is not charged').toBe(false)
    expect((await data.folio(guest.folio.id)).balance).toMatch(/^0(\.0+)?$/)
  })

  // KD-04 (docs/user-guide/_laporan.md): "Record a payment from the company" never paid an invoice, so an invoice the company had paid
  // stayed unpaid and overdue. The form now splits the amount over the open invoices from the oldest.
  test('NA-05 pembayaran dari perusahaan melunasi invoice jatuh temponya, dari yang tertua, dan invoice hilang dari daftar jatuh tempo', async ({ page, loginAs, data }) => {
    expect(debtor, 'NA-01 issued the invoices').toBeDefined()
    const company = debtor!
    await loginAs('admin', '/city-ledger/overdue')
    const overdue = page.getByTestId(`overdue-${company.code}`)
    for (const inv of company.invoices) await expect(overdue).toContainText(inv)

    const account = await data.api.get<{ balance: string }>(`${data.P}/city-ledger/accounts/${company.id}`)
    await page.goto(`/city-ledger/${company.id}`)
    const form = page.getByTestId('receipt-form')
    await form.locator('input[name=amount]').fill(account.balance) // one payment for both invoices
    const alloc = form.getByTestId('allocations')
    await expect(alloc.locator('[data-testid^=alloc-row-]')).toHaveCount(2)
    const rows = await data.api.get<{ data: { id: number; invoice_number: string; outstanding: string }[] }>(`${data.P}/city-ledger/accounts/${company.id}/invoices`)
    for (const inv of rows.data) await expect(form.locator(`input[name=alloc_${inv.id}]`)).toHaveValue(inv.outstanding.replace(/\.0+$/, ''))
    await form.locator('select[name=payment_method]').selectOption('BANK_TRANSFER')
    await form.locator('input[name=reference]').fill(`TRF-${uid()}`)
    await form.getByTestId('receipt-submit').click()
    await expect(page.getByTestId('notice')).toHaveText(tx('clAccount.receiptRecorded'))

    const after = await data.api.get<{ data: { invoice_number: string; payment_status: string; outstanding: string }[] }>(`${data.P}/city-ledger/accounts/${company.id}/invoices`)
    for (const inv of company.invoices) expect(after.data.find((i) => i.invoice_number === inv)?.payment_status, inv).toBe('PAID')
    await page.goto('/city-ledger/overdue')
    await expect(page.getByTestId('overdue-summary')).toBeVisible()
    await expect(page.getByTestId(`overdue-${company.code}`)).toHaveCount(0)
  })

  // KD-27 (docs/architecture/41-receipt-allocation.md): a receipt recorded without allocating it, on a day that is closed now, is allocated
  // to the invoices it paid: they become paid and leave the overdue list. The allocation is dated the new business date.
  test('NA-07 penerimaan yang belum dialokasikan dialokasikan setelah night audit, dan invoice hilang dari daftar jatuh tempo', async ({ page, loginAs, data }) => {
    expect(advance, 'NA-01 recorded the receipt on account').toBeDefined()
    const company = advance!
    const today = await data.businessDate()
    await loginAs('admin', '/city-ledger/overdue')
    const overdue = page.getByTestId(`overdue-${company.code}`)
    for (const inv of company.invoices) await expect(overdue).toContainText(inv)

    await page.goto(`/city-ledger/${company.id}`)
    await expect(page.getByTestId('account-unallocated')).toBeVisible()
    await expect(page.getByTestId(`left-${company.receipt}`)).toBeVisible()
    await page.getByTestId(`allocate-${company.receipt}`).click()
    const form = page.getByTestId('allocate-form')
    await expect(form.locator('[data-testid^=alloc-row-]')).toHaveCount(2)
    const rows = await data.api.get<{ data: { id: number; outstanding: string }[] }>(`${data.P}/city-ledger/accounts/${company.id}/invoices`)
    for (const inv of rows.data) await expect(form.locator(`input[name=alloc_${inv.id}]`)).toHaveValue(inv.outstanding.replace(/\.0+$/, ''))
    await form.getByTestId('allocate-submit').click()
    await expect(page.getByTestId('notice')).toContainText(company.receipt)
    await expect(page.getByTestId(`left-${company.receipt}`)).toHaveCount(0)
    await expect(page.getByTestId('account-unallocated')).toHaveCount(0)

    const after = await data.api.get<{ data: { invoice_number: string; payment_status: string }[] }>(`${data.P}/city-ledger/accounts/${company.id}/invoices`)
    for (const inv of company.invoices) expect(after.data.find((i) => i.invoice_number === inv)?.payment_status, inv).toBe('PAID')
    const receipts = await data.api.get<{ data: { receipt_number: string; business_date: string; allocations: { allocated_on: string }[] }[] }>(`${data.P}/city-ledger/accounts/${company.id}/receipts`)
    const r = receipts.data.find((x) => x.receipt_number === company.receipt)!
    expect(r.business_date).not.toBe(today) // the receipt stays on its closed day
    expect(r.allocations.map((a) => a.allocated_on)).toEqual([today, today])
    await page.goto('/city-ledger/overdue')
    await expect(page.getByTestId('overdue-summary')).toBeVisible()
    await expect(page.getByTestId(`overdue-${company.code}`)).toHaveCount(0)
  })

  // KD-05 (docs/architecture/40-card-settlement-void.md): a card settlement made with the wrong payment is voided with an approval, the payments are
  // settled again correctly, and the statement reconciles.
  test('NA-06 settlement kartu yang salah dibatalkan dengan persetujuan, diselesaikan ulang, lalu rekening koran direkonsiliasi', async ({ page, loginAs, data, seed }) => {
    expect(cards, 'NA-01 took the card payments').toBeDefined()
    const { day, payments } = cards!
    const P = data.P
    // a bank account of its own, so its first statement can reconcile whatever the other tests left in the other bank accounts
    const ledger = await data.api.post<{ id: number }>(`${P}/accounting/accounts`, { code: '1199', name: 'Bank settlement kartu (E2E)', account_type: 'ASSET', normal_side: 'DEBIT', statement_group: 'CASH', is_postable: true })
    const bank = await data.api.post<{ id: number }>(`${P}/bank/accounts`, { account_id: ledger.id, name: `Bank kartu ${uid()}`, account_number: `9${Date.now().toString().slice(-8)}` })
    const st = await data.api.post<{ id: number; lines: { id: number; line_no: number }[] }>(`${P}/bank/statements`, {
      bank_account_id: bank.id, period_from: day, period_to: day, opening_balance: '0', closing_balance: '800000',
      csv: `date,description,reference,amount\n${day},Settlement kartu A,SETT-A,300000\n${day},Settlement kartu B,SETT-B,500000\n`,
    })
    const [lineA, lineB] = [st.lines.find((l) => l.line_no === 1)!.id, st.lines.find((l) => l.line_no === 2)!.id]
    const expected = await data.api.get<{ lines: { journal_line_id: number; reference?: string }[] }>(`${P}/bank/card-settlements/expected`, { account_key: 'CARD' })
    const paymentLine = (n: string) => expected.lines.find((l) => l.reference === n)?.journal_line_id
    const [payA, payB] = [paymentLine(payments[0]!), paymentLine(payments[1]!)]
    expect(payA && payB, 'the card payments wait for their settlement').toBeTruthy()
    const commission = (await data.api.get<{ data: { id: number; code: string }[] }>(`${P}/accounting/accounts`, { account_type: 'EXPENSE', postable: true, active: true })).data.find((a) => a.code === '6130')!
    const settle = (line: number, payment: number, fee?: number) =>
      data.api.post(`${P}/bank/statements/${st.id}/lines/${line}/settle`, { account_key: 'CARD', journal_line_ids: [payment], fee_account_id: fee })

    // the wrong settlement: the 300,000 paid by the bank is taken for the payment of 500,000, with 200,000 of "commission"
    await settle(lineA, payB!, commission.id)
    const wrong = (await data.api.get<{ data: { journal_number: string; status: string }[] }>(`${P}/bank/card-settlements`, { limit: 1 })).data[0]!
    expect(wrong.status).toBe('POSTED')

    // its matching is not undone on the reconcile page: the link goes to the void
    await loginAs('accounting', `/bank/statements/${st.id}`)
    await expect(page.getByTestId('line-1').locator('[data-testid^="void-settlement-"]')).toBeVisible()
    await expect(page.getByTestId('line-1').locator('[data-testid^="unmatch-"]')).toHaveCount(0)

    // Void settlement, with a reason and the approval of someone who may approve corrections
    await page.goto('/bank/cards')
    await page.getByTestId(`void-${wrong.journal_number}`).click()
    const form = page.getByTestId('void-form')
    await form.locator('input[name=reason]').fill('Pembayaran yang dipilih salah (E2E)')
    await form.getByTestId('void-ask').click()
    const dialog = page.getByTestId('approval-dialog')
    await dialog.locator('input[name=approval_email]').fill(seed.users.admin.email)
    await dialog.locator('input[name=approval_password]').fill(seed.users.admin.password)
    await dialog.getByTestId('approval-submit').click()
    await expect(page.getByTestId('notice')).toHaveText(tx('cards.voided', { number: wrong.journal_number }))
    await expect(page.getByTestId(`voided-${wrong.journal_number}`)).toHaveText(tx('cards.statusVoided'))

    // the payments are released: settled again correctly, each with its own line, and the statement reconciles
    await settle(lineA, payA!)
    await settle(lineB, payB!)
    await page.goto(`/bank/statements/${st.id}`)
    await expect(page.getByTestId('ready')).toBeVisible()
    await page.getByTestId('reconcile').click()
    await expect(page.getByTestId('reconciled')).toBeVisible()
    const after = await data.api.get<{ status: string }>(`${P}/bank/statements/${st.id}`)
    expect(after.status).toBe('RECONCILED')
  })
})
