import { test, expect, settle } from '../support/fixtures'
import { uid } from '../support/factories'
import { tx } from '../support/i18n'
import { pickCombo, pickFirstCombo, setDate } from '../support/ui'
import type { Factories } from '../support/factories'

/** Back office finance: manual journal and trial balance, suppliers with a bill and its payment, bank accounts, budgets, tax. */

interface Account { id: number; code: string; name: string; account_type: string; is_postable: boolean; is_active: boolean; department_requirement: string }
async function accounts(data: Factories, type: string): Promise<Account[]> {
  return (await data.api.get<{ data: Account[] }>(`${data.P}/accounting/accounts`, { account_type: type, postable: true, active: true })).data
    .filter((a) => a.is_postable && a.is_active && a.department_requirement !== 'REQUIRED')
}

test.describe('Akuntansi dan back office', () => {
  test('ACC-01 jurnal manual seimbang diposting dan tampil di daftar jurnal', async ({ page, loginAs, data }) => {
    const assets = await accounts(data, 'ASSET')
    // petty cash and the payroll bank of the standard chart (migrations/00028): plain cash accounts, not control accounts
    const a = assets.find((x) => x.code === '1120') ?? assets[0]!
    const b = assets.find((x) => x.code === '1140') ?? assets[1]!
    const description = `Pindah kas ke bank ${uid()} (E2E)`
    await loginAs('accounting', '/accounting/journals')
    await page.getByTestId('new-journal').click()
    const form = page.getByTestId('journal-form')
    await setDate(form, 'date', await data.businessDate())
    await form.locator('input[name=reference]').fill('E2E-JV')
    await form.locator('input[name=description]').fill(description)
    await pickCombo(form.getByTestId('line-0'), 'account_0', `${a.code} ·`)
    await form.locator('input[name=debit_0]').fill('125000')
    await pickCombo(form.getByTestId('line-1'), 'account_1', `${b.code} ·`)
    await form.locator('input[name=credit_1]').fill('125000')
    await form.getByTestId('journal-post').click()
    await expect.poll(async () => (await data.api.get<{ data: { description: string; total: string; journal_type: string }[] }>(`${data.P}/accounting/journals`, { q: description })).data[0]).toMatchObject({ journal_type: 'MANUAL' })
    await expect(page.getByTestId('journals')).toContainText(description)
  })

  test('ACC-02 jurnal yang tidak seimbang tidak bisa diposting', async ({ page, loginAs, data }) => {
    const assets = (await accounts(data, 'ASSET')).filter((x) => x.code === '1120' || x.code === '1140')
    await loginAs('accounting', '/accounting/journals')
    await page.getByTestId('new-journal').click()
    const form = page.getByTestId('journal-form')
    await form.locator('input[name=description]').fill('Tidak seimbang (E2E)')
    await pickCombo(form.getByTestId('line-0'), 'account_0', `${assets[0]!.code} ·`)
    await form.locator('input[name=debit_0]').fill('100000')
    await pickCombo(form.getByTestId('line-1'), 'account_1', `${assets[1]!.code} ·`)
    await form.locator('input[name=credit_1]').fill('90000')
    await expect(form.getByTestId('difference')).toBeVisible()
    const post = form.getByTestId('journal-post')
    if (await post.isEnabled()) {
      await post.click()
      await expect(page.getByTestId('journal-error')).toBeVisible()
    }
    const found = (await data.api.get<{ data: unknown[] }>(`${data.P}/accounting/journals`, { q: 'Tidak seimbang (E2E)' })).data
    expect(found).toHaveLength(0)
  })

  test('ACC-03 neraca saldo seimbang: total debit = total kredit', async ({ page, loginAs, data }) => {
    await loginAs('accounting', '/accounting/trial-balance')
    await page.getByTestId('apply').click()
    await expect(page.getByTestId('trial-balance')).toBeVisible()
    await expect(page.getByTestId('totals')).toBeVisible()
    const bd = await data.businessDate()
    const tb = await data.api.get<{ totals: { closing_debit: string; closing_credit: string } }>(`${data.P}/accounting/trial-balance`, { from: `${bd.slice(0, 8)}01`, to: bd })
    expect(Number(tb.totals.closing_debit)).toBe(Number(tb.totals.closing_credit))
  })

  test('ACC-04 laporan laba rugi, neraca, dan arus kas terbuka tanpa error', async ({ page, loginAs }) => {
    await loginAs('accounting')
    for (const path of ['/accounting/income-statement', '/accounting/balance-sheet', '/accounting/cash-flow', '/accounting/ledger']) {
      await settle(page)
      await page.goto(path)
      await expect(page.locator('main h1')).toBeVisible()
      await expect(page.getByTestId('report-error')).toHaveCount(0)
    }
  })

  test('AP-01 membuat supplier baru', async ({ page, loginAs, data }) => {
    const code = `S${uid()}`.slice(0, 8)
    await loginAs('accounting', '/payables/suppliers')
    await page.getByTestId('new-supplier').click()
    const form = page.getByTestId('supplier-form')
    await form.locator('input[name=code]').fill(code)
    await form.locator('input[name=name]').fill(`CV Sumber Uji ${code}`)
    await form.locator('input[name=contact_name]').fill('Pak Ketut')
    await form.locator('input[name=payment_terms_days]').fill('14')
    const account = form.locator('[data-slot=combobox]').filter({ has: page.locator('select[name=default_account_id]') })
    if (await account.count()) await pickFirstCombo(form, 'default_account_id')
    await form.getByRole('button', { name: tx('common.save'), exact: true }).click()
    await expect(page.getByTestId(`supplier-${code}`)).toBeVisible()
    const all = (await data.api.get<{ data: { code: string; payment_terms_days: number }[] }>(`${data.P}/payables/suppliers`)).data
    expect(all.find((s) => s.code === code)?.payment_terms_days).toBe(14)
  })

  test('AP-02 tagihan supplier diposting lalu dibayar lunas', async ({ page, loginAs, data }) => {
    const expense = await accounts(data, 'EXPENSE')
    const supplier = await data.api.post<{ id: number; code: string; name: string }>(`${data.P}/payables/suppliers`, { code: `S${uid()}`.slice(0, 8), name: `PT Pemasok Uji ${uid()}`, payment_terms_days: 30, default_account_id: expense[0]!.id })
    const invoice = `INV-${uid()}`
    const bd = await data.businessDate()

    await loginAs('accounting', '/payables/bills')
    await page.getByTestId('new-bill').click()
    const form = page.getByTestId('bill-form')
    await pickCombo(form, 'supplier_id', supplier.code)
    await form.locator('input[name=invoice]').fill(invoice)
    await setDate(form, 'bill_date', bd)
    await form.locator('input[name=description]').fill('Pembelian perlengkapan (E2E)')
    await pickCombo(form.getByTestId('line-0'), 'account_0', `${expense[0]!.code} ·`)
    await form.locator('input[name=amount_0]').fill('500000')
    await form.getByTestId('bill-post').click()
    const findBill = async () => (await data.api.get<{ data: { supplier_invoice_number: string; payment_status: string; bill_number: string }[] }>(`${data.P}/payables/bills`, { q: invoice })).data.find((b) => b.supplier_invoice_number === invoice)
    await expect.poll(async () => (await findBill())?.payment_status).toBe('UNPAID')

    await page.goto('/payables/payments')
    await page.getByTestId('new-payment').click()
    const pay = page.getByTestId('payment-form')
    await pickCombo(pay, 'supplier_id', supplier.code)
    await setDate(pay, 'payment_date', bd)
    await expect(pay.getByTestId('open-bills')).toContainText((await findBill())!.bill_number)
    await pay.getByTestId('pay-all').click()
    await pay.getByTestId('payment-post').click()
    await expect.poll(async () => (await findBill())?.payment_status).toBe('PAID')
  })

  test('BNK-01 menambah rekening bank yang terhubung ke akun buku besar', async ({ page, loginAs, data }) => {
    const name = `Bank Uji ${uid()}`
    await loginAs('accounting', '/bank/accounts')
    await page.getByTestId('new-bank').click()
    const form = page.getByTestId('bank-form')
    await pickFirstCombo(form, 'account_id')
    await form.locator('input[name=name]').fill(name)
    await form.locator('input[name=account_number]').fill(`123${Date.now().toString().slice(-7)}`)
    await form.locator('button[type=submit]').click()
    await expect(page.getByTestId('banks')).toContainText(name)
    expect((await data.api.get<{ data: { name: string }[] }>(`${data.P}/bank/accounts`)).data.some((b) => b.name === name)).toBe(true)
  })

  test('BUD-01 membuat anggaran tahunan baru', async ({ page, loginAs, data }) => {
    const name = `Anggaran Uji ${uid()}`
    await loginAs('accounting', '/budget')
    await page.getByTestId('new-budget').click()
    const form = page.getByTestId('new-form')
    await form.locator('select[name=year_start]').selectOption(`${(await data.businessDate()).slice(0, 4)}-01-01`) // the fiscal year of the business date
    await form.locator('input[name=name]').fill(name)
    await form.locator('input[name=description]').fill('Dibuat oleh E2E')
    await form.getByTestId('create').click()
    await expect.poll(async () => (await data.api.get<{ data: { name: string }[] }>(`${data.P}/budgets`)).data.some((b) => b.name === name)).toBe(true)
  })

  test('TAX-01 halaman pajak menampilkan status dan profil pelaporan', async ({ page, loginAs }) => {
    await loginAs('accounting')
    for (const path of ['/tax/status', '/tax/profiles', '/tax/liability', '/tax/invoices', '/tax/returns']) {
      await settle(page)
      await page.goto(path)
      await expect(page.locator('main h1')).toBeVisible()
      await expect(page.getByTestId('no-access')).toHaveCount(0)
    }
  })

  test('ACC-05 resepsionis tidak bisa membuka jurnal (API 403)', async ({ apiAs, seed }) => {
    const fd = await apiAs('frontdesk')
    const err = await fd.get(`/properties/${seed.propertyId}/accounting/journals`).catch((e) => e)
    expect(err.status).toBe(403)
  })
})
