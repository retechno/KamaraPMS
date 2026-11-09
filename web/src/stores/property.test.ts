import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { usePropertyStore } from './property'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...args: unknown[]) => GET(...args) } }))

const bali = { id: 1, code: 'BALI', name: 'Hotel Bali' }
const jkt = { id: 2, code: 'JKT', name: 'Hotel Jakarta' }

function respond(list: object[]) {
  GET = vi.fn(async (path: string, opts?: { params?: { path?: { propertyId?: number } } }) => {
    const id = opts?.params?.path?.propertyId
    if (path === '/api/v1/properties') return { data: { data: list } }
    if (path === '/api/v1/properties/{propertyId}') return { data: { ...list.find((p) => (p as { id: number }).id === id), business_date: '2026-09-30' } }
    if (path === '/api/v1/properties/{propertyId}/business-date') return { data: { business_date: '2026-09-30' } }
    throw new Error('unexpected ' + path)
  })
}

describe('property store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
  })

  it('selects the first property and loads its business date', async () => {
    respond([bali, jkt])
    const store = usePropertyStore()
    await store.loadProperties()
    expect(store.currentId).toBe(1)
    expect(store.current?.business_date).toBe('2026-09-30')
    expect(store.clock?.business_date).toBe('2026-09-30')
    expect(localStorage.getItem('kamarapms.currentPropertyId')).toBe('1')
  })

  it('restores the remembered property', async () => {
    localStorage.setItem('kamarapms.currentPropertyId', '2')
    respond([bali, jkt])
    const store = usePropertyStore()
    await store.loadProperties()
    expect(store.currentId).toBe(2)
    expect(store.current?.code).toBe('JKT')
  })

  it('forgets a remembered property the user can no longer access', async () => {
    localStorage.setItem('kamarapms.currentPropertyId', '99')
    respond([bali])
    const store = usePropertyStore()
    await store.loadProperties()
    expect(store.currentId).toBe(1)
  })

  it('keeps going when the property cannot be read (a network failure is kept as an error, never thrown)', async () => {
    respond([bali])
    const inner = GET
    GET = vi.fn(async (path: string, opts?: object) => {
      if (path === '/api/v1/properties/{propertyId}') throw new TypeError('Failed to fetch')
      return inner(path, opts)
    })
    const store = usePropertyStore()
    await expect(store.loadProperties()).resolves.toBeUndefined()
    expect(store.currentId).toBe(1)
    expect(store.current).toBeNull()
    expect(store.error?.code).toBe('NETWORK_ERROR')
    expect(store.clock?.business_date).toBe('2026-09-30') // the clock is still read
  })

  it('a later select that succeeds clears the error of one that failed', async () => {
    respond([bali, jkt])
    const inner = GET
    let fail = true
    GET = vi.fn(async (path: string, opts?: object) => {
      if (path === '/api/v1/properties/{propertyId}' && fail) throw new TypeError('Failed to fetch')
      return inner(path, opts)
    })
    const store = usePropertyStore()
    await store.loadProperties()
    expect(store.error?.code).toBe('NETWORK_ERROR')
    fail = false
    await store.select(2)
    expect(store.error).toBeNull()
    expect(store.current?.code).toBe('JKT')
  })

  it('handles a tenant without properties', async () => {
    localStorage.setItem('kamarapms.currentPropertyId', '5')
    respond([])
    const store = usePropertyStore()
    await store.loadProperties()
    expect(store.hasProperties).toBe(false)
    expect(store.currentId).toBeNull()
    expect(localStorage.getItem('kamarapms.currentPropertyId')).toBeNull()
  })
})
