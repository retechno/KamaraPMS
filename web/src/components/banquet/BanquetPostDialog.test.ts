import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { setLocale } from '@/i18n'
import BanquetPostDialog, { type PostTarget } from './BanquetPostDialog.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const target = (over: Partial<PostTarget> = {}): PostTarget => ({
  event_id: 20, session_id: 31, event_number: 'BQ-000020', event_name: 'Lokakarya', session_name: 'Dinner', session_date: '2026-10-22', room_name: 'Saraswati A',
  pax_estimated: 45, pax_guaranteed: 40, pax_actual: null, ...over,
})

const mounted: VueWrapper[] = []
async function open(t: PostTarget) {
  setActivePinia(createPinia())
  const w = mount(BanquetPostDialog, { attachTo: document.body, props: { open: false, propertyId: 7, target: t, businessDate: '2026-10-22' } })
  mounted.push(w)
  await w.setProps({ open: true }) // the dialog reads the session when it opens
  await flushPromises()
  return w
}
const body = (sel: string) => new DOMWrapper(document.body.querySelector(sel) as Element)
const postBody = () => POST.mock.calls[0]?.[1]?.body

beforeEach(() => {
  setLocale('en')
  GET = vi.fn(async () => ({ data: { stock: null } }))
  POST = vi.fn(async () => ({ data: { total: '0' } }))
})
afterEach(() => {
  mounted.splice(0).forEach((w) => w.unmount())
  document.body.innerHTML = ''
})

// KD-03 (docs/user-guide/_laporan.md): the number typed in "Pax aktual" was dropped. The input is type=number, so the model holds a
// number and `.trim()` threw: the billed number stayed the guaranteed one and the posting went out without the actual guests.
describe('BanquetPostDialog: the guests that came', () => {
  it('bills 42 when 42 came for 40 guaranteed, and sends the 42 with the posting', async () => {
    await open(target({ pax_guaranteed: 40 }))
    await body('input[name=pax_actual]').setValue('42')
    expect(body('[data-testid=post-billed]').text()).toBe('42')
    expect(body('[data-testid=post-basis]').text()).toMatch(/guests that came/i)
    await body('[data-testid=post-save]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[0]?.[0]).toBe('/api/v1/properties/{propertyId}/banquet/events/{id}/sessions/{sid}/post')
    expect(postBody()).toEqual({ pax_actual: 42, use_estimate: false })
  })

  it('bills the guarantee when fewer came, and still sends the actual number (it is kept on the session)', async () => {
    await open(target({ pax_guaranteed: 40 }))
    await body('input[name=pax_actual]').setValue('35')
    expect(body('[data-testid=post-billed]').text()).toBe('40')
    expect(body('[data-testid=post-basis]').text()).toMatch(/guarantee/i)
    await body('[data-testid=post-save]').trigger('click')
    await flushPromises()
    expect(postBody()).toEqual({ pax_actual: 35, use_estimate: false })
  })

  it('sends no actual number when the field is left empty, and starts from the number already on the session', async () => {
    await open(target({ pax_guaranteed: 40, pax_actual: 44 }))
    expect((body('input[name=pax_actual]').element as HTMLInputElement).value).toBe('44')
    expect(body('[data-testid=post-billed]').text()).toBe('44')
    await body('input[name=pax_actual]').setValue('')
    expect(body('[data-testid=post-billed]').text()).toBe('40')
    await body('[data-testid=post-save]').trigger('click')
    await flushPromises()
    expect(postBody()).toEqual({ pax_actual: null, use_estimate: false })
  })

  it('a session with no number is posted on the estimate only when the person says so', async () => {
    await open(target({ pax_guaranteed: null }))
    expect(body('[data-testid=post-save]').attributes('disabled')).toBeDefined()
    await body('input[name=use_estimate]').setValue(true)
    await body('[data-testid=post-save]').trigger('click')
    await flushPromises()
    expect(postBody()).toEqual({ pax_actual: null, use_estimate: true })
  })
})
