import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import GuestsView from './GuestsView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const siti = { id: 1, code: 'GST000001', first_name: 'Siti', last_name: 'Nurhaliza', email: 'siti@mail.com', nationality: 'ID' }

function mountView(permissions: string[], firstPage: object = { data: [siti] }) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data: firstPage })
  POST = vi.fn()
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(GuestsView, { global: { plugins: [pinia, router] }, attachTo: document.body })
}

/** Runs `fn` with the window of a phone (390 px), then puts the width back. */
async function onAPhone<T>(fn: () => Promise<T>): Promise<T> {
  const wide = window.innerWidth
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: 390 })
  try {
    return await fn()
  } finally {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: wide })
  }
}

describe('GuestsView', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
  })

  it('lists guests on open and searches within the current property', async () => {
    const w = mountView(['guest.read'])
    await flushPromises()
    expect(GET).toHaveBeenLastCalledWith('/api/v1/guests', { params: { query: { q: '', property_id: 7, limit: 50, cursor: undefined } } })
    expect(w.get('[data-testid=guest-GST000001]').text()).toContain('Siti Nurhaliza')

    await w.get('input[name=q]').setValue('nurha')
    await w.get('form[role=search]').trigger('submit')
    await flushPromises()
    expect(GET).toHaveBeenLastCalledWith('/api/v1/guests', { params: { query: { q: 'nurha', property_id: 7, limit: 50, cursor: undefined } } })
  })

  it('says so when nothing matches', async () => {
    const w = mountView(['guest.read'])
    await flushPromises()
    GET.mockResolvedValue({ data: { data: [] } })
    await w.get('form[role=search]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=empty]').text()).toContain('No guests found.')
  })

  it('appends the next page', async () => {
    const w = mountView(['guest.read'], { data: [siti], next_cursor: 'abc' })
    await flushPromises()
    GET.mockResolvedValue({ data: { data: [{ ...siti, id: 2, code: 'GST000002', last_name: 'Wu' }] } })
    await w.get('[data-testid=more]').trigger('click')
    await flushPromises()
    expect(GET).toHaveBeenLastCalledWith('/api/v1/guests', { params: { query: { q: '', property_id: 7, limit: 50, cursor: 'abc' } } })
    expect(w.findAll('tbody tr')).toHaveLength(2)
  })

  it('does not query without guest.read', async () => {
    const w = mountView([])
    await flushPromises()
    expect(GET).not.toHaveBeenCalled()
    expect(w.find('[data-testid=no-access]').exists()).toBe(true)
  })

  it('creates a guest with only the filled fields and warns about duplicates, including hidden ones', async () => {
    const w = mountView(['guest.read', 'guest.write'])
    await flushPromises()
    POST.mockResolvedValue({
      data: {
        id: 9, code: 'GST000009', last_name: 'Nurhaliza',
        possible_duplicates: [{ guest: siti, reasons: ['SAME_EMAIL'] }], hidden_duplicate_count: 2,
      },
    })
    await w.get('[data-testid=new-guest]').trigger('click')
    await w.get('input[name=last_name]').setValue('Nurhaliza')
    await w.get('input[name=email]').setValue('siti@mail.com')
    await w.get('[data-testid=create-form]').trigger('submit')
    await flushPromises()

    expect(POST).toHaveBeenCalledWith('/api/v1/guests', { body: { origin_property_id: 7, last_name: 'Nurhaliza', email: 'siti@mail.com' } })
    expect(w.get('[data-testid=created]').text()).toContain('GST000009')
    expect(w.get('[data-testid=duplicates]').text()).toContain('same email')
    expect(w.get('[data-testid=hidden-duplicates]').text()).toContain('2 similar profile(s)')
  })

  it('shows field errors from the server', async () => {
    const w = mountView(['guest.read', 'guest.write'])
    await flushPromises()
    POST.mockRejectedValue(
      new ApiError({
        type: 't', title: 'Unprocessable Entity', status: 422, code: 'VALIDATION_FAILED', detail: 'the guest is invalid',
        errors: [{ field: 'email', code: 'INVALID_FORMAT', message: 'a valid email address' }],
      }),
    )
    await w.get('[data-testid=new-guest]').trigger('click')
    await w.get('input[name=email]').setValue('nope')
    await w.get('[data-testid=create-form]').trigger('submit')
    await flushPromises()
    expect(w.get('input[name=email]').attributes('aria-invalid')).toBe('true')
    expect(w.text()).toContain('a valid email address')
  })

  it('hides creation without guest.write', async () => {
    const w = mountView(['guest.read'])
    await flushPromises()
    expect(w.find('[data-testid=new-guest]').exists()).toBe(false)
  })
  it('starts on the search field, and on the first name when the new guest form opens', async () => {
    const w = mountView(['guest.read', 'guest.write'])
    await flushPromises()
    expect((document.activeElement as HTMLInputElement | null)?.name).toBe('q')
    await w.get('[data-testid=new-guest]').trigger('click')
    await flushPromises()
    expect((document.activeElement as HTMLInputElement | null)?.name).toBe('first_name')
    w.unmount()
  })

  it('opens the guest when the row is clicked, and counts what is loaded', async () => {
    const w = mountView(['guest.read'])
    await flushPromises()
    const router = (w.vm as unknown as { $router: { currentRoute: { value: { path: string } } } }).$router
    expect(w.get('[data-testid=table-count]').text()).toBe('1 loaded')
    await w.get(`[data-testid=guest-${siti.code}] td:nth-child(3)`).trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe(`/guests/${siti.id}`)
  })

  it('is a card on a phone: the code as the title, the name under it', async () => {
    await onAPhone(async () => {
      const w = mountView(['guest.read'])
      await flushPromises()
      expect(w.find('table').exists()).toBe(false)
      const card = w.get(`[data-testid=guest-${siti.code}]`)
      expect(card.attributes('data-slot')).toBe('data-card')
      expect(card.text()).toContain(siti.code)
      w.unmount()
    })
  })
})
