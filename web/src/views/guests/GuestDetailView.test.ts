import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import GuestDetailView from './GuestDetailView.vue'

let GET = vi.fn()
let PATCH = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), PATCH: (...a: unknown[]) => PATCH(...a) } }))

const guest = { id: 4, code: 'GST000004', first_name: 'Siti', last_name: 'Nurhaliza', email: 'siti@mail.com', city: 'Ubud', can_edit: true }
const item = {
  type: 'STAY', id: 1, number: 'STY000001', role: 'primary', status: 'CHECKED_OUT', property_id: 7, property_code: 'BALI',
  property_name: 'Bali', arrival_date: '2026-09-28', departure_date: '2026-10-01',
}

function mountView(over: { guest?: object; history?: object } = {}) {
  setActivePinia(createPinia())
  GET = vi.fn(async (path: string) =>
    path.endsWith('/history')
      ? { data: { data: [item], hidden_count: 2, ...over.history } }
      : { data: { ...guest, ...over.guest } },
  )
  PATCH = vi.fn()
  return mount(GuestDetailView, { props: { id: '4' } })
}

describe('GuestDetailView', () => {
  beforeEach(() => {
    GET = vi.fn()
    PATCH = vi.fn()
  })

  it('shows the profile, the history and how many entries are hidden', async () => {
    const w = mountView()
    await flushPromises()
    expect((w.get('input[name=last_name]').element as HTMLInputElement).value).toBe('Nurhaliza')
    expect(w.get('tbody').text()).toContain('STY000001')
    expect(w.get('tbody').text()).toContain('28 Sep 2026 to 1 Oct 2026')
    expect(w.get('[data-testid=hidden]').text()).toContain('2 more entries at other properties are not shown')
  })

  it('saves every field so cleared ones are cleared on the server', async () => {
    const w = mountView()
    await flushPromises()
    PATCH.mockResolvedValue({ data: { ...guest, city: '' } })
    await w.get('input[name=city]').setValue('')
    await w.get('form').trigger('submit')
    await flushPromises()
    const [path, opts] = PATCH.mock.calls[0] as [string, { body: Record<string, string> }]
    expect(path).toBe('/api/v1/guests/{id}')
    expect(opts.body).toMatchObject({ last_name: 'Nurhaliza', city: '', phone: '' })
    expect(w.find('[data-testid=saved]').exists()).toBe(true)
  })

  it('is read-only without edit rights', async () => {
    const w = mountView({ guest: { can_edit: false } })
    await flushPromises()
    expect(w.find('[data-testid=read-only]').exists()).toBe(true)
    expect(w.get('input[name=last_name]').attributes('disabled')).toBeDefined()
    expect(w.find('button[type=submit]').exists()).toBe(false)
  })

  it('reports an invisible guest as not found', async () => {
    setActivePinia(createPinia())
    GET = vi.fn().mockRejectedValue(new ApiError({ type: 't', title: 'Not Found', status: 404, code: 'GUEST_NOT_FOUND', detail: 'x' }))
    const w = mount(GuestDetailView, { props: { id: '4' } })
    await flushPromises()
    expect(w.find('[data-testid=not-found]').exists()).toBe(true)
  })

  it('shows server errors on save', async () => {
    const w = mountView()
    await flushPromises()
    PATCH.mockRejectedValue(new ApiError({ type: 't', title: 'Forbidden', status: 403, code: 'PERMISSION_DENIED', detail: 'guest.write is required' }))
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=form-error]').text()).toContain('PERMISSION_DENIED')
  })
})
