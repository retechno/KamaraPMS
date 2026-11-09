import { test, expect } from '../support/fixtures'
import { addDays, uid } from '../support/factories'
import { tx } from '../support/i18n'
import { pickCombo, setDate } from '../support/ui'

/** Corporate accounts, group bookings and the city ledger (a folio sent to a company, then the company's receipt). */
test.describe('Corporate, grup, dan city ledger', () => {
  test('CORP-01 membuat akun perusahaan dengan limit kredit', async ({ page, loginAs, data }) => {
    const code = `PT${uid()}`.slice(0, 8)
    await loginAs('admin', '/setup/companies')
    await page.getByTestId('new-company').click()
    const form = page.getByTestId('company-form')
    await form.locator('input[name=code]').fill(code)
    await form.locator('input[name=name]').fill(`PT Nusa Uji ${code}`)
    await form.locator('input[name=contact_name]').fill('Ibu Ratna')
    await form.locator('input[name=email]').fill(`finance.${code.toLowerCase()}@e2e.example`)
    await form.locator('input[name=unlimited]').uncheck() // a new company starts without a limit: the field is off until this is cleared
    await form.locator('input[name=credit_limit]').fill('25000000')
    await form.locator('input[name=payment_terms_days]').fill('30')
    await form.getByRole('button', { name: tx('common.save'), exact: true }).click()
    await expect(page.getByTestId(`company-${code}`)).toBeVisible()
    const companies = (await data.api.get<{ data: { code: string; credit_limit: string | null; payment_terms_days: number }[] }>(`${data.P}/companies`)).data
    expect(companies.find((c) => c.code === code)).toMatchObject({ payment_terms_days: 30 })
    expect(Number(companies.find((c) => c.code === code)?.credit_limit)).toBe(25000000)
  })

  test('GRP-01 membuat grup dan membuka detailnya', async ({ page, loginAs, data }) => {
    const company = await data.company()
    const code = `G${uid()}`.slice(0, 8)
    const arrival = addDays(await data.businessDate(), 14)
    await loginAs('admin', '/groups')
    await page.getByTestId('new-group').click()
    const form = page.getByTestId('group-form')
    await form.locator('input[name=code]').fill(code)
    await form.locator('input[name=name]').fill(`Rombongan Uji ${code}`)
    await setDate(form, 'arrival_date', arrival)
    await setDate(form, 'departure_date', addDays(arrival, 3))
    await pickCombo(form, 'company_id', company.code)
    await form.locator('input[name=contact_name]').fill('Pak Made')
    await form.getByRole('button', { name: tx('common.save'), exact: true }).click()
    await expect(page.getByTestId(`group-${code}`)).toBeVisible()
    await page.getByTestId(`group-${code}`).getByRole('link').first().click()
    await expect(page.getByTestId('group-summary')).toContainText(code)
  })

  test('GRP-02 reservasi yang terhubung ke grup tampil di detail grup', async ({ page, loginAs, data }) => {
    const arrival = addDays(await data.businessDate(), 15)
    const group = await data.api.post<{ id: number; code: string }>(`${data.P}/groups`, { code: `G${uid()}`.slice(0, 8), name: 'Grup reservasi E2E', arrival_date: arrival, departure_date: addDays(arrival, 2) })
    const res = await data.reservation({ arrival, groupId: group.id })
    await loginAs('frontdesk', `/groups/${group.id}`)
    await expect(page.getByTestId(`member-${res.confirmation_number}`)).toBeVisible()
  })

  test('CL-01 saldo folio dipindah ke city ledger perusahaan lalu dilunasi lewat penerimaan', async ({ page, loginAs, data }) => {
    const company = await data.company()
    const ih = await data.inHouse()
    await data.postCharge(ih.folio.id, 'FOOD_BEVERAGE', '300000', 'Rapat makan siang (E2E)')
    const balance = (await data.folio(ih.folio.id)).balance

    await loginAs('admin', `/folios/${ih.folio.id}?tab=transfer`)
    const form = page.getByTestId('transfer-form')
    await pickCombo(form, 'transfer_company', company.code)
    await form.locator('input[name=transfer_amount]').fill(balance)
    await form.locator('input[name=transfer_reference]').fill(`PO-${uid()}`)
    await form.getByRole('button', { name: tx('folio.transferButton') }).click()
    await expect.poll(async () => Number((await data.folio(ih.folio.id)).balance)).toBe(0)

    const accounts = (await data.api.get<{ data: { company_id: number; balance: string }[] }>(`${data.P}/city-ledger/accounts`, { limit: 200 })).data
    const account = accounts.find((a) => a.company_id === company.id)
    expect(Number(account?.balance)).toBe(Number(balance))

    await page.goto(`/city-ledger/${company.id}`) // the account of a company is addressed by the company id
    await expect(page.getByTestId('balance')).toBeVisible()
    const receipt = page.getByTestId('receipt-form')
    await receipt.locator('input[name=amount]').fill(balance)
    await receipt.locator('select[name=payment_method]').selectOption('BANK_TRANSFER')
    await receipt.locator('input[name=reference]').fill(`TRF-${uid()}`)
    await receipt.getByRole('button', { name: tx('clAccount.recordReceipt') }).click()
    await expect.poll(async () => Number((await data.api.get<{ balance: string }>(`${data.P}/city-ledger/accounts/${company.id}`)).balance)).toBe(0)
  })

  test('CL-02 daftar city ledger bisa difilter yang masih berutang', async ({ page, loginAs }) => {
    await loginAs('admin', '/city-ledger')
    await expect(page.getByTestId('filters')).toBeVisible()
    await page.getByTestId('filters').locator('input[name=owing]').check()
    await expect(page.getByTestId('form-error')).toHaveCount(0)
  })
})
