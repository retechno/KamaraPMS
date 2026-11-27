import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { defineComponent, h, ref } from 'vue'
import AllocationTable from './AllocationTable.vue'
import { checkAllocations, openInvoicesOf, splitFromOldest } from './allocation'

const inv = (id: number, number: string, date: string, outstanding: string, status = 'ISSUED') =>
  ({ id, invoice_number: number, company_id: 1, invoice_date: date, due_date: date, total: outstanding, credited: '0', written_off: '0', paid: '0', outstanding, payment_status: 'UNPAID', status }) as never
const older = inv(61, 'CINV000061', '2026-09-25', '300000')
const newer = inv(51, 'CINV000001', '2026-09-30', '400000')

describe('the split of a company payment over its open invoices', () => {
  it('offers the issued invoices that still owe, the oldest first', () => {
    const list = openInvoicesOf([newer, inv(50, 'VOID', '2026-09-01', '10', 'VOIDED'), inv(49, 'PAID', '2026-09-02', '0'), older])
    expect(list.map((i: { id: number }) => i.id)).toEqual([61, 51])
  })

  it('fills from the oldest, in thousandths', () => {
    expect(splitFromOldest('500000.5', [older, newer])).toEqual({ 61: '300000', 51: '200000.5' })
    expect(splitFromOldest('', [older, newer])).toEqual({ 61: '', 51: '' })
  })

  it('says what is wrong and sends only the lines that pay something', () => {
    expect(checkAllocations({ 61: '350000' }, [older, newer], '400000').problem).toContain('CINV000061')
    expect(checkAllocations({ 61: 'abc' }, [older, newer], '400000').problem).toBe('An allocation is not an amount.')
    expect(checkAllocations({ 61: '300000', 51: '200000' }, [older, newer], '400000').problem).toBe('The allocations are more than the amount received.')
    const ok = checkAllocations({ 61: '', 51: '100000' }, [older, newer], '150000')
    expect(ok.problem).toBe('')
    expect(ok.unallocated).toBe(50000000n)
    expect(ok.payload).toEqual([{ invoice_id: 51, amount: '100000' }])
  })
})

describe('AllocationTable', () => {
  const host = (amount: string) => {
    const model = ref<Record<number, string>>(splitFromOldest(amount, [older, newer])) // the parent gives the first split
    const amt = ref(amount)
    const w = mount(defineComponent({ setup: () => () => h(AllocationTable, { amount: amt.value, invoices: [older, newer], modelValue: model.value, 'onUpdate:modelValue': (v: Record<number, string>) => { model.value = v } }) }))
    return { w, model, amt }
  }
  const value = (w: ReturnType<typeof mount>, id: number) => (w.get(`input[name=alloc_${id}]`).element as HTMLInputElement).value

  it('fills from the oldest and follows the amount until the clerk types', async () => {
    const { w, model, amt } = host('500000')
    await w.vm.$nextTick()
    expect(model.value).toEqual({ 61: '300000', 51: '200000' })
    await w.vm.$nextTick()
    expect(value(w, 61)).toBe('300000') // the first split is shown, not only kept
    amt.value = '800000'
    await w.vm.$nextTick()
    await w.vm.$nextTick()
    expect(value(w, 51)).toBe('400000')
    expect(w.get('[data-testid=unallocated]').text()).toContain('100,000')
    await w.get('input[name=alloc_61]').setValue('')
    amt.value = '700000'
    await w.vm.$nextTick()
    expect(value(w, 61)).toBe('') // kept
    await w.get('[data-testid=allocate-oldest]').trigger('click')
    expect(model.value).toEqual({ 61: '300000', 51: '400000' })
  })

  it('shows nothing without an open invoice', () => {
    const w = mount(AllocationTable, { props: { amount: '1', invoices: [], modelValue: {} } })
    expect(w.find('[data-testid=allocations]').exists()).toBe(false)
  })
})
