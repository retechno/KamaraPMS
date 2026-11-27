import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { setLocale } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import ReservationEditDialog from './ReservationEditDialog.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))

const draft = (guest: { id: number; code: string; first_name?: string; last_name: string } | null) => ({
  id: 40, confirmation_number: 'R000040', status: 'DRAFT', display_status: 'DRAFT', version: 2, guest_id: guest?.id ?? null, guest, company_id: null, company_name: null,
  special_request: null, remarks: null, rooms: [], folios: [],
})

let mounted: VueWrapper | null = null
const body = () => document.body

async function mountDialog(reservation: unknown) {
  const pinia = createPinia()
  setActivePinia(pinia)
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data: { data: [{ id: 12, code: 'G000012', first_name: 'Siti', last_name: 'Nurhaliza' }] } })
  mounted = mount(ReservationEditDialog, {
    props: { open: true, reservation: reservation as never, types: [], ratePlans: [], companies: [], busy: false, error: null },
    attachTo: document.body, global: { plugins: [pinia] },
  })
  await flushPromises()
  return mounted
}

async function type(selector: string, value: string): Promise<void> {
  const el = body().querySelector(selector) as HTMLInputElement
  el.value = value
  el.dispatchEvent(new Event('input'))
  await flushPromises()
}
const click = async (selector: string) => {
  ;(body().querySelector(selector) as HTMLElement).click()
  await flushPromises()
}

describe('ReservationEditDialog', () => {
  beforeEach(() => setLocale('en'))
  afterEach(() => {
    mounted?.unmount()
    mounted = null
    document.body.innerHTML = ''
  })

  // A draft made without a booker could not be confirmed (BOOKER_REQUIRED), and no screen could set one: Edit reservation now takes the booker.
  it('sets the booker of a draft that has none, sending only guest_id', async () => {
    const w = await mountDialog(draft(null))
    expect(body().querySelector('[data-testid=edit-booker]')!.textContent).toContain('not set')
    await type('input[name=booker_q]', 'siti')
    await click('[data-testid=find-booker]')
    expect(GET).toHaveBeenCalledWith('/api/v1/guests', { params: { query: { q: 'siti', property_id: 7, limit: 10 } } })
    await click('[data-testid=booker-G000012]')
    expect(body().querySelector('[data-testid=edit-booker]')!.textContent).toContain('Siti Nurhaliza')
    await click('[data-testid=save-header-fields]')
    expect(w.emitted('saveHeader')?.[0]).toEqual([{ guest_id: 12 }])
  })

  it('shows the booker already set and sends nothing while it is unchanged', async () => {
    await mountDialog(draft({ id: 9, code: 'G000009', first_name: 'Budi', last_name: 'Santoso' }))
    expect(body().querySelector('[data-testid=edit-booker]')!.textContent).toContain('Budi Santoso')
    expect((body().querySelector('[data-testid=save-header-fields]') as HTMLButtonElement).disabled).toBe(true)
  })
})
