import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import GroupRoomingList from './GroupRoomingList.vue'

let GET = vi.fn()
let PATCH = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), PATCH: (...a: unknown[]) => PATCH(...a), POST: (...a: unknown[]) => POST(...a) } }))

const row = (n: number, over: object = {}) => ({
  reservation_id: 10 + n, confirmation_number: `RES00000${n}`, reservation_status: 'CONFIRMED', version: 1, line_id: 100 + n, line_status: 'CONFIRMED', room_type: 'DLX', arrival_date: '2026-10-02',
  departure_date: '2026-10-04', guest_id: 50 + n, name: `Conference · ${n}`, email: '', phone: '', is_placeholder: true, ...over,
})

const mounted: VueWrapper[] = []
function mountList(permissions = ['reservation.read', 'guest.read', 'reservation.update', 'guest.write'], rows = [row(1), row(2, { name: 'Ayu Lestari', email: 'ayu@alpha.test', is_placeholder: false })]) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async () => ({ data: { data: rows } }))
  PATCH = vi.fn(async () => ({ data: { updated: 1, unchanged: 0 } }))
  POST = vi.fn()
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  const w = mount(GroupRoomingList, { props: { groupId: 4 }, global: { plugins: [pinia, router] }, attachTo: document.body })
  mounted.push(w)
  return w
}

const problem = (code: string, fields: { field: string; code: string; message: string }[] = [], context: object = {}) => new ApiError({ code, title: 'invalid', status: 422, errors: fields, context } as never)

describe('GroupRoomingList', () => {
  beforeEach(() => setLocale('en'))
  afterEach(() => {
    mounted.splice(0).forEach((w) => w.unmount())
    document.body.innerHTML = ''
  })

  it('lists every room with its guest, the placeholder muted with a badge', async () => {
    const w = mountList()
    await flushPromises()
    expect(w.get('[data-testid=rooming-row-RES000001]').text()).toContain('DLX')
    expect(w.get('[data-testid=placeholder-RES000001]').text()).toBe('No name yet')
    expect(w.find('[data-testid=placeholder-RES000002]').exists()).toBe(false)
    expect(w.get('[data-testid=rooming-name-RES000001]').classes()).toContain('text-muted-foreground')
    expect((w.get('[data-testid=rooming-email-RES000002]').element as HTMLInputElement).value).toBe('ayu@alpha.test')
    expect(GET.mock.calls[0]?.[1].params.path).toEqual({ propertyId: 7, id: 4 })
  })

  it('saves only the rows that changed, all at once, and reads the list again', async () => {
    const w = mountList()
    await flushPromises()
    expect(w.get('[data-testid=rooming-save]').attributes('disabled')).toBeDefined()
    await w.get('[data-testid=rooming-name-RES000001]').setValue('Budi Santoso')
    await w.get('[data-testid=rooming-phone-RES000001]').setValue('+62 811 1111 2222')
    await w.get('[data-testid=rooming-save]').trigger('click')
    await flushPromises()
    expect(PATCH.mock.calls[0]?.[1].body).toEqual({ rows: [{ line_id: 101, name: 'Budi Santoso', email: '', phone: '+62 811 1111 2222' }] })
    expect(w.get('[data-testid=rooming-notice]').text()).toBe('1 guests updated.')
    expect(w.emitted('changed')).toHaveLength(1)
    expect(GET).toHaveBeenCalledTimes(2)
  })

  it('shows a refused row where it is and keeps what was typed', async () => {
    const w = mountList()
    await flushPromises()
    PATCH = vi.fn(async () => {
      throw problem('VALIDATION_FAILED', [{ field: 'rows[0].email', code: 'INVALID_FORMAT', message: 'not an e-mail' }])
    })
    await w.get('[data-testid=rooming-email-RES000001]').setValue('nope')
    await w.get('[data-testid=rooming-save]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=rooming-row-RES000001]').text()).toContain('not an e-mail')
    expect((w.get('[data-testid=rooming-email-RES000001]').element as HTMLInputElement).value).toBe('nope')
  })

  it('checks a file first, reports every wrong row, and applies only a clean one', async () => {
    const w = mountList()
    await flushPromises()
    const input = w.get('[data-testid=rooming-file]').element as HTMLInputElement
    const file = new File(['confirmation_number,name\nRES000001,Budi\nNOPE,Who\n'], 'list.csv', { type: 'text/csv' })
    Object.defineProperty(input, 'files', { value: [file] })
    await w.get('[data-testid=rooming-file]').trigger('change')
    await flushPromises()
    POST = vi.fn(async () => ({
      data: {
        dry_run: true, rows: 2, applied: false,
        changes: [{ line_id: 101, confirmation_number: 'RES000001', from_name: 'Conference · 1', to_name: 'Budi', from_email: '', to_email: '', from_phone: '', to_phone: '' }],
        problems: [{ row: 3, column: 'confirmation_number', code: 'NOT_IN_GROUP', message: 'x' }],
      },
    }))
    await w.get('[data-testid=rooming-check]').trigger('click')
    await flushPromises()
    const call = POST.mock.calls[0]
    expect(call?.[1].params.query).toEqual({ dry_run: true })
    expect(call?.[1].headers).toEqual({ 'Content-Type': 'text/csv' })
    expect(call?.[1].body).toContain('RES000001,Budi')
    expect(w.get('[data-testid=rooming-check-result]').text()).toContain('2 rows read: 1 to change, 1 wrong.')
    expect(w.get('[data-testid=rooming-problem-3]').text()).toContain('Line 3')
    expect(w.get('[data-testid=rooming-problem-3]').text()).toContain('Not a reservation of this group.')
    expect(w.get('[data-testid=rooming-apply]').attributes('disabled')).toBeDefined() // not with a wrong row
    // the same file without the wrong row
    POST = vi.fn(async (_p: string, o: { params: { query: { dry_run: boolean } } }) => ({
      data: { dry_run: o.params.query.dry_run, rows: 1, applied: !o.params.query.dry_run, problems: [], changes: [{ line_id: 101, confirmation_number: 'RES000001', from_name: 'Conference · 1', to_name: 'Budi', from_email: '', to_email: '', from_phone: '', to_phone: '' }] },
    }))
    await w.get('[data-testid=rooming-check]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=rooming-apply]').attributes('disabled')).toBeUndefined()
    await w.get('[data-testid=rooming-apply]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls.at(-1)?.[1].params.query).toEqual({ dry_run: false })
    expect(w.get('[data-testid=rooming-notice]').text()).toBe('The file was applied: 1 guests updated.')
  })

  it('is read-only without the right to edit, and absent without the right to read', async () => {
    const reader = mountList(['reservation.read', 'guest.read'])
    await flushPromises()
    expect((reader.get('[data-testid=rooming-name-RES000001]').element as HTMLInputElement).disabled).toBe(true)
    expect(reader.find('[data-testid=rooming-save]').exists()).toBe(false)
    expect(reader.find('[data-testid=rooming-import]').exists()).toBe(false)
    const nobody = mountList(['reservation.read'])
    await flushPromises()
    expect(nobody.find('[data-testid=rooming-list]').exists()).toBe(false)
    expect(GET).not.toHaveBeenCalled()
  })

  it('says it in Indonesian', async () => {
    setLocale('id')
    const w = mountList()
    await flushPromises()
    expect(w.get('[data-testid=placeholder-RES000001]').text()).toBe('Belum bernama')
    expect(w.text()).toContain('Rooming list')
    setLocale('en')
  })
})
