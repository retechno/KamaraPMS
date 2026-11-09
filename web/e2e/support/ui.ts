import { expect, type Locator, type Page } from 'playwright/test'
import { displayDate } from './factories'

type Scope = Page | Locator

/** Types a date into a DateInput (components/ui/date-input): the text field takes dd/mm/yyyy; the hidden native field holds the `name`. */
export async function setDate(scope: Scope, name: string, iso: string): Promise<void> {
  const field = scope.locator('[data-slot=date-input]').filter({ has: scope.locator(`input[name="${name}"]`) }).locator('[data-slot=date-input-text]')
  await field.fill(displayDate(iso))
  await field.blur()
}

/** Chooses an option of a Combobox (components/ui/combobox) by its label (or a part of it). The combobox mirrors its value in a hidden <select name>. */
export async function pickCombo(scope: Scope, name: string, label: string | RegExp): Promise<void> {
  const box = scope.locator('[data-slot=combobox]').filter({ has: scope.locator(`select[name="${name}"]`) })
  const input = box.locator('input').first()
  await input.click()
  if (typeof label === 'string') await input.fill(label)
  const page = 'page' in scope ? scope.page() : scope
  await page.locator('[data-slot=combobox-content] [role=option]').filter({ hasText: label }).first().click()
}

/** Chooses the first real option of a Combobox (skips a placeholder such as "Choose an account", which has the value 0). */
export async function pickFirstCombo(scope: Scope, name: string, skip = 0): Promise<string> {
  const box = scope.locator('[data-slot=combobox]').filter({ has: scope.locator(`select[name="${name}"]`) })
  const options = box.locator(`select[name="${name}"] option:not([value="0"]):not([disabled])`)
  await expect(options.nth(skip)).toBeAttached()
  const value = (await options.nth(skip).getAttribute('value')) ?? ''
  const label = (await options.nth(skip).textContent())?.trim() ?? ''
  await pickCombo(scope, name, label)
  return value
}

/** Chooses an option of a native <select name> by its visible label (or a part of it). */
export async function pickSelect(scope: Scope, name: string, label: string | RegExp): Promise<void> {
  const select = scope.locator(`select[name="${name}"]`)
  await expect(select.locator('option').filter({ hasText: label }).first()).toBeAttached()
  const value = await select.locator('option').filter({ hasText: label }).first().getAttribute('value')
  await select.selectOption(value ?? { label: String(label) })
}

/** The success toast that a step shows (ToastHost: data-testid toast-success). */
export async function expectSuccessToast(page: Page, text?: string | RegExp): Promise<void> {
  const toast = page.getByTestId('toast-success').last()
  await expect(toast).toBeVisible()
  if (text) await expect(page.getByTestId('toast-success').filter({ hasText: text }).first()).toBeVisible()
}

export async function expectNoErrorToast(page: Page): Promise<void> {
  await expect(page.getByTestId('toast-error')).toHaveCount(0)
}

/** Confirms the dialog of useConfirm (components/app/feedback). */
export async function confirmDialog(page: Page): Promise<void> {
  await page.getByTestId('confirm-dialog').getByTestId('confirm-ok').click()
}

/** A page opened without a 403 or an error notice: the heading is there and no "no access" block. */
export async function expectPageReady(page: Page): Promise<void> {
  await expect(page.locator('main h1, main [data-testid=page-title], [data-testid=page-title]').first()).toBeVisible()
  await expect(page.getByTestId('no-access')).toHaveCount(0)
}

/** The value of a number shown with Indonesian grouping ("1.250.000"), as a number. */
export function idNumber(text: string): number {
  const m = text.replace(/\s/g, '').match(/-?[\d.]+(,\d+)?/)
  return m ? Number(m[0].replace(/\./g, '').replace(',', '.')) : NaN
}
