import { DOMWrapper, flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import { ApiError } from '@/api/problem'
import type { ApprovalKind } from '@/api/types'
import { useApprovalRulesStore } from '@/stores/approvalRules'
import { useAuthStore } from '@/stores/auth'
import ApprovalDialog from './ApprovalDialog.vue'

// The dialog says the rule of the property for the correction (docs/architecture/32-distinct-approver.md): nothing for FREE, someone else for DISTINCT, and for DISTINCT_ABOVE it depends on the amount.
const $ = (sel: string) => new DOMWrapper(document.body.querySelector(sel) as Element)
const exists = (sel: string) => document.body.querySelector(sel) !== null

type R = { rule: 'FREE' | 'DISTINCT' | 'DISTINCT_ABOVE'; threshold?: string }
async function mountDialog(rule: R | null, props: Record<string, unknown> = {}, admin = false) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'clerk@hotel.com', is_tenant_admin: admin }, properties: [] } as never
  if (rule) useApprovalRulesStore().rules = [{ kind: 'REFUND' as ApprovalKind, rule: rule.rule, threshold: rule.threshold ?? null }]
  const w = mount(ApprovalDialog, { props: { title: 'Approve refund', kind: 'REFUND', ...props }, global: { plugins: [pinia] }, attachTo: document.body })
  await flushPromises()
  return w
}
const emailValue = () => ($('input[name=approval_email]').element as HTMLInputElement).value

describe('ApprovalDialog and the rule of the property', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
  })

  it('says nothing for a kind that is free, and keeps the email of the person', async () => {
    await mountDialog({ rule: 'FREE' }, { amount: '100' })
    expect(exists('[data-testid=approval-rule]')).toBe(false)
    expect(emailValue()).toBe('clerk@hotel.com')
  })

  it('says nothing when the kind is not named (a screen that does not know it)', async () => {
    await mountDialog({ rule: 'DISTINCT' }, { kind: undefined })
    expect(exists('[data-testid=approval-rule]')).toBe(false)
    expect(emailValue()).toBe('clerk@hotel.com')
  })

  it('DISTINCT: someone else, and the email is left empty', async () => {
    await mountDialog({ rule: 'DISTINCT' }, { amount: '100' })
    expect($('[data-testid=approval-rule]').attributes('data-rule')).toBe('distinct')
    expect($('[data-testid=approval-rule]').text()).toContain('someone other than you')
    expect(emailValue()).toBe('')
  })

  it('DISTINCT_ABOVE: above the limit someone else, at or under it their own', async () => {
    await mountDialog({ rule: 'DISTINCT_ABOVE', threshold: '500000' }, { amount: '500001' })
    expect($('[data-testid=approval-rule]').attributes('data-rule')).toBe('above')
    expect($('[data-testid=approval-rule]').text()).toContain('Above 500,000')
    expect(emailValue()).toBe('')
    document.body.innerHTML = ''
    await mountDialog({ rule: 'DISTINCT_ABOVE', threshold: '500000' }, { amount: '500000' })
    expect($('[data-testid=approval-rule]').attributes('data-rule')).toBe('within')
    expect($('[data-testid=approval-rule]').text()).toContain('Up to 500,000 you may approve it yourself')
    expect(emailValue()).toBe('clerk@hotel.com')
  })

  it('DISTINCT_ABOVE without the amount: the limit is said and the email stays', async () => {
    await mountDialog({ rule: 'DISTINCT_ABOVE', threshold: '500000' })
    expect($('[data-testid=approval-rule]').attributes('data-rule')).toBe('limit')
    expect(emailValue()).toBe('clerk@hotel.com')
  })

  it('a tenant administrator keeps their email and is told it is recorded', async () => {
    await mountDialog({ rule: 'DISTINCT' }, { amount: '100' }, true)
    expect($('[data-testid=approval-rule]').text()).toContain('As an administrator you may approve your own')
    expect(emailValue()).toBe('clerk@hotel.com')
    document.body.innerHTML = ''
    await mountDialog({ rule: 'DISTINCT_ABOVE', threshold: '1000' }, { amount: '1001' }, true)
    expect($('[data-testid=approval-rule]').text()).toContain('Above 1,000')
    expect($('[data-testid=approval-rule]').text()).toContain('administrator')
  })

  it('a refusal of the rule names the limit from the context', async () => {
    const error = new ApiError({ type: 't', title: 'Forbidden', status: 403, code: 'APPROVAL_SAME_USER', detail: 'the rule asks for another approver', context: { threshold: '500000', amount: '600000' } } as never)
    await mountDialog({ rule: 'DISTINCT_ABOVE', threshold: '500000' }, { amount: '600000', error })
    expect($('[data-testid=approval-same-user]').text()).toContain('Above 500,000')
  })

  it('does not wipe the email the person has typed when the rule arrives', async () => {
    const w = await mountDialog(null, { amount: '100' })
    await $('input[name=approval_email]').setValue('manager@hotel.com')
    useApprovalRulesStore().rules = [{ kind: 'REFUND' as ApprovalKind, rule: 'DISTINCT', threshold: null }]
    await flushPromises()
    expect(emailValue()).toBe('manager@hotel.com')
    w.unmount()
  })
})
