import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, describe, expect, it } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { en } from '@/i18n/locales/en'
import { id } from '@/i18n/locales/id'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import ReadinessFindings from './ReadinessFindings.vue'

// The codes of the go-live check, as the server declares them (internal/accounting/readiness.go).
const codes = [...readFileSync(resolve(__dirname, '../../../../internal/accounting/readiness.go'), 'utf8').matchAll(/^\s+(?:Block|Warn)\w+\s+= "([A-Z_]+)"/gm)].map((m) => m[1]!)

async function mountList(items: { code: string; message: string; ref?: string }[], permissions: string[], severity: 'blocker' | 'warning' = 'blocker') {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'a@b.c', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  const w = mount(ReadinessFindings, { props: { items, severity }, global: { plugins: [pinia, router] } })
  await flushPromises()
  return w
}

// Item 10 of PR C2: the night audit showed these findings as raw codes with the English sentence of the server, and the go-live check had no page.
describe('the findings of the accounting readiness', () => {
  afterEach(() => setLocale('en'))

  it('has words in English and Indonesian for every code the server sends', () => {
    expect(codes.length).toBe(10)
    for (const code of codes) {
      expect((en.readiness.code as Record<string, string>)[code], `en ${code}`).toBeTruthy()
      expect((id.readiness.code as Record<string, string>)[code], `id ${code}`).toBeTruthy()
    }
  })

  it('writes the finding in the language of the page, not the code or the English sentence of the server', async () => {
    await setLocale('id')
    const w = await mountList([{ code: 'ACCOUNT_MAP_MISSING', ref: 'CASH', message: 'the system account CASH is not mapped' }], ['accounting.view'])
    const text = w.get('[data-testid=readiness-ACCOUNT_MAP_MISSING]').text()
    expect(text).toContain('Akun sistem CASH')
    expect(text).not.toContain('ACCOUNT_MAP_MISSING')
    expect(text).not.toContain('is not mapped')
  })

  it('links each finding to the screen that fixes it, for who may open that screen', async () => {
    const w = await mountList([
      { code: 'ACCOUNT_MAP_UNUSABLE', ref: 'CARD', message: '' },
      { code: 'ROOM_CHARGE_CODE_UNMAPPED', ref: 'ROOM', message: '' },
      { code: 'DEPARTMENT_SETUP_INCOMPLETE', ref: 'LAUNDRY', message: '' },
      { code: 'TRANSFER_ACCOUNT_NOT_ZERO', ref: 'FOLIO_TRANSFER', message: '' },
    ], ['accounting.view', 'billing_config.manage'])
    expect(w.get('[data-testid=readiness-ACCOUNT_MAP_UNUSABLE] a').attributes('href')).toBe('/accounting/mapping')
    expect(w.get('[data-testid=readiness-ROOM_CHARGE_CODE_UNMAPPED] a').attributes('href')).toBe('/setup/charge-codes')
    expect(w.get('[data-testid=readiness-DEPARTMENT_SETUP_INCOMPLETE] a').attributes('href')).toBe('/accounting/departments')
    expect(w.get('[data-testid=readiness-TRANSFER_ACCOUNT_NOT_ZERO] a').attributes('href')).toBe('/accounting/journals')
    expect(w.text()).not.toContain('Ask an accountant')
  })

  it('asks for an accountant instead of a link the night auditor may not open', async () => {
    const w = await mountList([
      { code: 'ACCOUNT_MAP_MISSING', ref: 'CASH', message: '' },
      { code: 'ROOM_CHARGE_CODE_UNMAPPED', ref: 'ROOM', message: '' },
    ], ['nightaudit.run'])
    expect(w.find('a').exists()).toBe(false)
    expect(w.get('[data-testid=readiness-ACCOUNT_MAP_MISSING]').text()).toContain('Ask an accountant to fix it.')
    expect(w.get('[data-testid=readiness-ROOM_CHARGE_CODE_UNMAPPED]').text()).toContain('Ask an accountant to fix it.')
  })

  it('says that the accounting data is incomplete and points to the support team, with no screen to fix it', async () => {
    const w = await mountList([
      { code: 'ACCOUNTING_NOT_SET_UP', message: '' },
      { code: 'ACCOUNTING_NOT_STARTED', ref: '2026-10-05', message: '' },
      { code: 'JOURNAL_SEQUENCE_MISSING', message: '' },
    ], ['accounting.view', 'accounting.manage'])
    expect(w.find('a').exists()).toBe(false)
    for (const code of ['ACCOUNTING_NOT_SET_UP', 'ACCOUNTING_NOT_STARTED', 'JOURNAL_SEQUENCE_MISSING']) {
      const text = w.get(`[data-testid=readiness-${code}]`).text()
      expect(text, code).toContain('incomplete restore')
      expect(text, code).toContain('Help and contact')
      expect(text, code).not.toContain('Ask an accountant')
    }
    expect(w.get('[data-testid=readiness-ACCOUNTING_NOT_STARTED]').text()).toContain('5 Oct 2026')
  })

  it('falls back on the sentence of the server for a code this release does not know', async () => {
    const w = await mountList([{ code: 'NEW_CHECK', message: 'something new is wrong' }], ['accounting.view'])
    expect(w.get('[data-testid=readiness-NEW_CHECK]').text()).toContain('something new is wrong')
  })
})
