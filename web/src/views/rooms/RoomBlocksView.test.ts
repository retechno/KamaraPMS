import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import RoomBlocksView from './RoomBlocksView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const rooms = [
  { id: 1, room_type_id: 1, room_number: '201', is_active: true },
  { id: 2, room_type_id: 1, room_number: '202', is_active: true },
]
const block = {
  id: 9, room_id: 2, block_type: 'OOO', start_date: '2026-10-02', end_date: '2026-10-05', reason: 'AC repair', status: 'ACTIVE',
}

function mountCalendar(permissions: string[]) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-10-01' } as never
  GET = vi.fn(async (path: string) => ({ data: { data: path.endsWith('/rooms') ? rooms : [block] } }))
  POST = vi.fn().mockResolvedValue({ data: {} })
  return mount(RoomBlocksView, { global: { plugins: [pinia] } })
}

describe('RoomBlocksView', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
  })

  it('loads active blocks for the window starting at the business date and draws them on the room row', async () => {
    const w = mountCalendar(['room_block.manage'])
    await flushPromises()
    const blockCall = GET.mock.calls.find(([p]) => (p as string).endsWith('/room-blocks'))
    expect(blockCall?.[1]).toMatchObject({ params: { query: { status: 'ACTIVE', from: '2026-10-01', to: '2026-10-15' } } })
    expect(w.findAll('[data-testid=cal-201] [data-testid=bar]')).toHaveLength(0)
    const bar = w.get('[data-testid=cal-202] [data-testid=bar]')
    // 2 Oct is the 2nd day of the window: grid column 3 (column 1 is the label), end exclusive 5 Oct = column 6.
    expect(bar.attributes('style')).toContain('grid-column: 3 / 6')
    expect(bar.text()).toBe('OOO')
  })

  it('creates a block and reloads', async () => {
    const w = mountCalendar(['room_block.manage'])
    await flushPromises()
    await w.get('select[name=room_id]').setValue(1)
    await w.get('select[name=block_type]').setValue('OOS')
    await w.get('input[name=start_date]').setValue('2026-10-03')
    await w.get('input[name=end_date]').setValue('2026-10-04')
    await w.get('input[name=reason]').setValue('Paint')
    await w.get('[data-testid=block-form]').trigger('submit')
    await flushPromises()

    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/room-blocks', {
      params: { path: { propertyId: 7 } },
      body: { room_id: 1, block_type: 'OOS', start_date: '2026-10-03', end_date: '2026-10-04', reason: 'Paint' },
    })
  })

  it('lists the stays that conflict with a rejected block', async () => {
    const w = mountCalendar(['room_block.manage'])
    await flushPromises()
    POST.mockRejectedValue(
      new ApiError({
        type: 't', title: 'Conflict', status: 409, code: 'ROOM_BLOCK_CONFLICT', detail: 'the room is occupied',
        context: { conflicts: [{ type: 'STAY', id: 3, reference: 'STY000003', from: '2026-10-01', to: '2026-10-03' }] },
      }),
    )
    await w.get('[data-testid=block-form]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=form-error]').text()).toContain('ROOM_BLOCK_CONFLICT')
    expect(w.get('[data-testid=conflicts]').text()).toContain('STY000003')
  })

  it('releases a block with a reason', async () => {
    const w = mountCalendar(['room_block.manage'])
    await flushPromises()
    await w.get('[data-testid=block-9] button').trigger('click')
    await w.get('input[name=cancel_reason]').setValue('Repair done')
    await w.get('[data-testid=cancel-form]').trigger('submit')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/room-blocks/{id}/cancel', {
      params: { path: { propertyId: 7, id: 9 } },
      body: { reason: 'Repair done' },
    })
  })

  it('is read-only without room_block.manage', async () => {
    const w = mountCalendar([])
    await flushPromises()
    expect(w.find('[data-testid=block-form]').exists()).toBe(false)
    expect(w.find('[data-testid=block-9] button').exists()).toBe(false)
    expect(w.find('[data-testid=cal-202] [data-testid=bar]').exists()).toBe(true)
  })

  it('names the weekday over each date, shades the weekend and highlights the business date', async () => {
    const w = mountCalendar(['room_block.manage'])
    await flushPromises()
    const heads = w.findAll('[role=columnheader]')
    expect(heads[0]!.text()).toContain('Thu') // 1 Oct 2026
    expect(heads[0]!.classes()).toContain('bg-primary/10')
    expect(heads[2]!.text()).toContain('Sat')
    expect(heads[2]!.classes()).toContain('bg-muted/60')
  })

  it('shows the type of a block as a badge in the list, and a calendar bar by type', async () => {
    const w = mountCalendar(['room_block.manage'])
    await flushPromises()
    expect(w.get('[data-testid=block-9]').text()).toContain('Out of order')
    expect(w.get('[data-testid=cal-202] [data-testid=bar]').classes()).toEqual(expect.arrayContaining(['status-hatch', 'border-status-ooo'])) // the same stripe as an out of order room on the other pages
    expect(w.get('[data-testid=block-9] [data-slot=status-badge]').classes()).toContain('status-hatch')
  })

  it('speaks Indonesian', async () => {
    setLocale('id')
    const w = mountCalendar(['room_block.manage'])
    await flushPromises()
    expect(w.get('h1').text()).toBe('Blokir kamar')
    expect(w.get('[data-testid=block-form]').text()).toContain('Blokir baru')
    expect(w.get('[data-testid=block-9] button').text()).toBe('Lepaskan')
    setLocale('en')
  })
})
