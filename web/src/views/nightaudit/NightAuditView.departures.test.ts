import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { emitApiMutation } from '@/api/mutationEvents'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { useNightAuditStore } from '@/stores/nightAudit'
import { usePropertyStore } from '@/stores/property'
import { toastText } from '@/test/toasts'
import NightAuditView from './NightAuditView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const PREVIEW = '/api/v1/properties/{propertyId}/night-audit/preview'
const STAY = '/api/v1/properties/{propertyId}/stays/{id}'

const folio = (balance: string) => ({ id: 1, folio_number: 'F1', folio_type: 'GUEST', bill_to_company_id: null, status: 'OPEN', balance })
const owing = { amount: '150000', status: 'OUTSTANDING', folios: [folio('150000')] }
const credit = { amount: '-50000', status: 'CREDIT', folios: [folio('-50000')] }
const settled = { amount: '0', status: 'SETTLED', folios: [folio('0')] }
const noFolio = { amount: '', status: 'NO_FOLIO', folios: [] }

const departure = (id: number, balance: object) => ({ stay_id: id, stay_number: `STY00000${id}`, guest: `Guest ${id}`, room: `10${id}`, departure_date: '2026-09-30', balance })

function preview(departures: object[]) {
  return {
    business_date: '2026-09-30', property_local_time: '2026-09-30T20:30:00+07:00', time_guard_ok: true, night_audit_allowed_from: '2026-09-30T13:00:00Z', can_run: departures.length === 0,
    blockers: { unresolved_arrivals: [], unresolved_departures: departures, charge_errors: [], invalid_charges: [] },
    missing_charges: { count: 0, items: [] }, tonight_charges: { count: 0, total: '0' },
    warnings: { stale_drafts: [], open_folios_of_cancelled_reservations: [], blocks_ending: [] },
  }
}

const four = [departure(5, owing), departure(6, credit), departure(7, noFolio), departure(8, settled)]

const detail = (id: number) => ({ stay: { id, stay_number: `STY00000${id}`, status: 'OPEN', version: 3, departure_date: '2026-09-30' }, folios: [] })

/** The previews the page reads, in order; the last one is read again and again. */
function serve(previews: object[], stay: (id: number) => unknown = (id) => ({ data: detail(id) })) {
  let n = 0
  GET = vi.fn().mockImplementation(async (path: string, opts?: { params?: { path?: { id?: number } } }) => {
    if (path === PREVIEW) return { data: previews[Math.min(n++, previews.length - 1)] }
    if (path === STAY) return stay(opts?.params?.path?.id ?? 0)
    return { data: {} }
  })
}

const previewReads = (): number => GET.mock.calls.filter((c) => c[0] === PREVIEW).length
const stayReads = (): number => GET.mock.calls.filter((c) => c[0] === STAY).length
const bodyEl = (sel: string) => document.body.querySelector(sel)

let mounted: VueWrapper | null = null

/** The wizard and the stay actions are the components of the stay page, tested there: here they are stubs that say "done", "changed" and "failed" on request. */
async function mountView(permissions = ['nightaudit.run', 'frontdesk.checkout', 'reservation.update']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-09-30' } as never
  POST = vi.fn()
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  mounted = mount(NightAuditView, {
    attachTo: document.body,
    global: {
      plugins: [pinia, router],
      stubs: {
        CheckOutWizard: {
          props: ['detail'], emits: ['done', 'cancel', 'failed'],
          template: `<div data-testid="checkout-wizard"><span data-testid="wizard-stay">{{ detail.stay.id }}:{{ detail.stay.version }}</span>
            <button data-testid="stub-done" @click="$emit('done', { posted_room_charges: [] })" />
            <button data-testid="stub-mutation" @click="mutate()" />
            <button data-testid="stub-conflict" @click="$emit('failed', conflict())" /></div>`,
          methods: {
            mutate: () => emitApiMutation('POST', '/api/v1/properties/7/stays/5/check-out'),
            conflict: () => new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'VERSION_CONFLICT', detail: 'changed' }),
          },
        },
        StayActions: {
          props: ['detail', 'initial'], emits: ['changed'],
          template: `<div data-testid="stay-actions" :data-initial="initial"><button data-testid="stub-changed" @click="$emit('changed', 'Departure is now 2026-10-02.')" /><button data-testid="stub-stale" @click="$emit('changed', '')" /></div>`,
        },
      },
    },
  })
  await flushPromises()
  return mounted
}

describe('NightAuditView, the unfinished departures', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
    setLocale('en')
  })

  afterEach(() => {
    mounted?.unmount()
    mounted = null
    document.body.innerHTML = ''
  })

  describe('the buttons of a row', () => {
    it('are the check-out and the extension, each with its own permission', async () => {
      serve([preview(four)])
      let w = await mountView()
      for (const id of [5, 6, 7, 8]) {
        expect(w.find(`[data-testid=check-out-${id}]`).exists()).toBe(true)
        expect(w.find(`[data-testid=extend-${id}]`).exists()).toBe(true)
      }
      w.unmount()
      w = await mountView(['nightaudit.run', 'frontdesk.checkout'])
      expect(w.find('[data-testid=check-out-5]').exists()).toBe(true)
      expect(w.find('[data-testid=extend-5]').exists()).toBe(false)
      w.unmount()
      w = await mountView(['nightaudit.run', 'reservation.update'])
      expect(w.find('[data-testid=check-out-5]').exists()).toBe(false)
      expect(w.find('[data-testid=extend-5]').exists()).toBe(true)
      w.unmount()
      w = await mountView(['nightaudit.run'])
      expect(w.find('[data-testid=departure-5]').exists()).toBe(true) // the row is there
      expect(w.find('[data-testid=check-out-5]').exists()).toBe(false)
      expect(w.find('[data-testid=extend-5]').exists()).toBe(false)
      expect(w.get('[data-testid=departures]').text()).not.toContain('Actions') // no column of nothing
    })

    it('say "Pay & check out", in an outline with an amber wallet, exactly when the stay owes money', async () => {
      serve([preview(four)])
      const w = await mountView()
      const owes = w.get('[data-testid=check-out-5]')
      expect(owes.text()).toBe('Pay & check out')
      expect(owes.classes()).toContain('border') // outline
      expect(owes.find('svg.text-warning-text').exists()).toBe(true)
      for (const id of [6, 7, 8]) {
        const b = w.get(`[data-testid=check-out-${id}]`)
        expect(b.text(), `stay ${id}`).toBe('Check out')
        expect(b.classes(), `stay ${id}`).toContain('bg-primary') // solid
        expect(b.find('svg').exists()).toBe(false)
      }
    })

    it('are followed by the balance, in the warning colour when it is owed', async () => {
      serve([preview(four)])
      const w = await mountView()
      expect(w.get('[data-testid=departure-balance-5]').text()).toContain('150')
      expect(w.get('[data-testid=departure-balance-5]').attributes('data-owes')).toBe('true')
      expect(w.get('[data-testid=departure-balance-5]').classes()).toContain('text-warning-text')
      expect(w.get('[data-testid=departure-balance-6]').text()).toContain('credit')
      expect(w.get('[data-testid=departure-balance-6]').attributes('data-owes')).toBe('false')
      expect(w.get('[data-testid=departure-balance-7]').text()).toBe('No folio')
      expect(w.get('[data-testid=departure-balance-8]').classes()).not.toContain('text-warning-text')
    })
  })

  describe('the check-out', () => {
    it('reads the stay and opens the wizard of the stay page in a sheet, in place', async () => {
      serve([preview(four)])
      const w = await mountView()
      await w.get('[data-testid=check-out-6]').trigger('click')
      await flushPromises()
      expect(GET).toHaveBeenCalledWith(STAY, { params: { path: { propertyId: 7, id: 6 } } })
      expect(bodyEl('[data-testid=departure-sheet]')?.textContent).toContain('Guest 6')
      expect(bodyEl('[data-testid=wizard-stay]')?.textContent).toBe('6:3')
      expect(bodyEl('[data-testid=stay-actions]')).toBeNull()
    })

    it('reads the steps again when it is done: the row is gone, the step and the count follow, the sheet closes', async () => {
      serve([preview([departure(5, owing)]), preview([])])
      const w = await mountView()
      expect(w.get('[data-testid=readiness]').text()).toBe('1 step(s) need attention')
      await w.get('[data-testid=check-out-5]').trigger('click')
      await flushPromises()
      const before = previewReads()
      ;(bodyEl('[data-testid=stub-done]') as HTMLElement).click()
      await flushPromises()
      expect(previewReads()).toBe(before + 1)
      expect(bodyEl('[data-testid=departure-sheet]')).toBeNull()
      expect(w.find('[data-testid=departure-5]').exists()).toBe(false)
      expect(w.get('[data-testid=readiness]').text()).toBe('Ready to run')
      expect(useNightAuditStore().preview?.blockers.unresolved_departures).toEqual([]) // the chip and the Today card read the same
      expect(toastText()).toContain('Checked out')
    })

    it('is a write that makes the shared preview stale, and the page reads it again', async () => {
      serve([preview([departure(5, owing)]), preview([])])
      const w = await mountView()
      const store = useNightAuditStore()
      const version = store.version
      await w.get('[data-testid=check-out-5]').trigger('click')
      await flushPromises()
      ;(bodyEl('[data-testid=stub-mutation]') as HTMLElement).click() // the client tells that a check-out went through
      expect(store.version).toBe(version + 1)
      const before = previewReads()
      ;(bodyEl('[data-testid=stub-done]') as HTMLElement).click()
      await flushPromises()
      expect(previewReads()).toBe(before + 1)
    })

    it('keeps the sheet and reads the stay and the steps again when the wizard fails on a conflict', async () => {
      let version = 3
      serve([preview([departure(5, owing)])], (id) => ({ data: { ...detail(id), stay: { ...detail(id).stay, version: version++ } } }))
      const w = await mountView()
      await w.get('[data-testid=check-out-5]').trigger('click')
      await flushPromises()
      expect(bodyEl('[data-testid=wizard-stay]')?.textContent).toBe('5:3')
      const reads = { stay: stayReads(), preview: previewReads() }
      ;(bodyEl('[data-testid=stub-conflict]') as HTMLElement).click()
      await flushPromises()
      expect(stayReads()).toBe(reads.stay + 1)
      expect(previewReads()).toBe(reads.preview + 1)
      expect(bodyEl('[data-testid=departure-sheet]')).not.toBeNull() // the clerk sees the error the wizard shows and may try again
      expect(bodyEl('[data-testid=wizard-stay]')?.textContent).toBe('5:4') // with the version as it is now
    })

    it('says so, and opens nothing, when the stay cannot be read', async () => {
      serve([preview(four)], () => {
        throw new ApiError({ type: 't', title: 'Not found', status: 404, code: 'STAY_NOT_FOUND', detail: 'no such stay' })
      })
      const w = await mountView()
      await w.get('[data-testid=check-out-5]').trigger('click')
      await flushPromises()
      expect(bodyEl('[data-testid=departure-sheet]')).toBeNull()
      expect(toastText()).toContain('no such stay')
    })
  })

  describe('the extension', () => {
    it('opens the stay actions of the stay page, on the extension, in a sheet', async () => {
      serve([preview(four)])
      const w = await mountView()
      await w.get('[data-testid=extend-7]').trigger('click')
      await flushPromises()
      expect(GET).toHaveBeenCalledWith(STAY, { params: { path: { propertyId: 7, id: 7 } } })
      expect(bodyEl('[data-testid=departure-sheet]')?.textContent).toContain('Extend the stay of Guest 7')
      expect(bodyEl('[data-testid=stay-actions]')?.getAttribute('data-initial')).toBe('extend')
      expect(bodyEl('[data-testid=wizard-stay]')).toBeNull()
    })

    it('reads the steps again when the departure is changed: the row is gone, the sheet closes, the clerk is told', async () => {
      serve([preview([departure(5, owing), departure(6, settled)]), preview([departure(6, settled)])])
      const w = await mountView()
      await w.get('[data-testid=extend-5]').trigger('click')
      await flushPromises()
      const before = previewReads()
      ;(bodyEl('[data-testid=stub-changed]') as HTMLElement).click()
      await flushPromises()
      expect(previewReads()).toBe(before + 1)
      expect(bodyEl('[data-testid=departure-sheet]')).toBeNull()
      expect(w.find('[data-testid=departure-5]').exists()).toBe(false)
      expect(w.find('[data-testid=departure-6]').exists()).toBe(true)
      expect(toastText()).toContain('Departure is now 2026-10-02.')
    })

    it('keeps the sheet and reads again when the stay changed under it (a conflict: nothing was changed)', async () => {
      serve([preview([departure(5, owing)])])
      const w = await mountView()
      await w.get('[data-testid=extend-5]').trigger('click')
      await flushPromises()
      const reads = { stay: stayReads(), preview: previewReads() }
      ;(bodyEl('[data-testid=stub-stale]') as HTMLElement).click()
      await flushPromises()
      expect(stayReads()).toBe(reads.stay + 1)
      expect(previewReads()).toBe(reads.preview + 1)
      expect(bodyEl('[data-testid=departure-sheet]')).not.toBeNull()
    })
  })

  describe('on a phone', () => {
    async function onAPhone<T>(fn: () => Promise<T>): Promise<T> {
      const wide = window.innerWidth
      Object.defineProperty(window, 'innerWidth', { configurable: true, value: 390 })
      try {
        return await fn()
      } finally {
        Object.defineProperty(window, 'innerWidth', { configurable: true, value: wide })
      }
    }

    it('is a card with the check-out as the one button and the extension in the "..." menu', async () => {
      serve([preview([departure(5, owing)])])
      await onAPhone(async () => {
        const w = await mountView()
        const card = w.get('[data-testid=departure-5]')
        expect(card.attributes('data-slot')).toBe('data-card')
        expect(card.findAll('[data-slot=row-actions] button[data-testid]').map((b) => b.attributes('data-testid'))).toEqual(['check-out-5'])
        expect(card.get('[data-testid=check-out-5]').text()).toBe('Pay & check out')
        expect(card.text()).toContain('150') // the balance
        await card.get('[data-slot=row-menu-trigger]').trigger('click')
        await flushPromises()
        const items = Array.from(document.body.querySelectorAll('[data-slot=row-menu] [data-testid]')).map((e) => e.getAttribute('data-testid'))
        expect(items).toEqual(['extend-5'])
        ;(bodyEl('[data-testid=extend-5]') as HTMLElement).click()
        await flushPromises()
        expect(bodyEl('[data-testid=stay-actions]')).not.toBeNull()
      })
    })
  })

  it('says in the help where the buttons are, in both languages', async () => {
    serve([preview(four)])
    const w = await mountView()
    expect(w.get('[data-testid=departures]').text()).toContain('with the buttons on the row')
    w.unmount()
    setLocale('id')
    serve([preview(four)])
    const id = await mountView()
    expect(id.get('[data-testid=departures]').text()).toContain('dengan tombol di baris')
    expect(id.get('[data-testid=check-out-5]').text()).toBe('Bayar & check-out')
  })
})
