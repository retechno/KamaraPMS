import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { defineComponent, h } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { useServerSort } from './useServerSort'

const ALLOWED = ['balance', 'folio_number'] as const

async function mountWith(url: string) {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/folios', component: { template: '<div />' } }] })
  await router.push(url)
  let api!: ReturnType<typeof useServerSort>
  const Probe = defineComponent({
    setup() {
      api = useServerSort(ALLOWED)
      return () => h('div')
    },
  })
  mount(Probe, { global: { plugins: [router] } })
  return { router, api: () => api }
}

describe('useServerSort', () => {
  it('reads the order from the address, ascending when only the column is given', async () => {
    const { api } = await mountWith('/folios?sort=balance&order=desc')
    expect(api().sort.value).toEqual({ key: 'balance', dir: 'desc' })
    expect(api().params.value).toEqual({ sort: 'balance', order: 'desc' })
    const b = await mountWith('/folios?sort=folio_number')
    expect(b.api().sort.value).toEqual({ key: 'folio_number', dir: 'asc' })
  })

  it('is no order (the list\'s own) without a sort, or with a column the server does not know', async () => {
    for (const url of ['/folios', '/folios?order=desc', '/folios?sort=password&order=asc', '/folios?sort=balance;drop']) {
      const { api } = await mountWith(url)
      expect(api().sort.value, url).toBeNull()
      expect(api().params.value, url).toEqual({})
    }
  })

  it('writes the order to the address as a new entry of the history, leaves the other parameters alone, and Back returns to the order before', async () => {
    const { router, api } = await mountWith('/folios?status=OPEN')
    api().setSort({ key: 'balance', dir: 'desc' })
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ status: 'OPEN', sort: 'balance', order: 'desc' })
    expect(api().sort.value).toEqual({ key: 'balance', dir: 'desc' })
    api().setSort({ key: 'folio_number', dir: 'asc' })
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ status: 'OPEN', sort: 'folio_number', order: 'asc' })
    router.back()
    await flushPromises()
    expect(api().sort.value).toEqual({ key: 'balance', dir: 'desc' }) // Back: the order before
    api().setSort(null)
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ status: 'OPEN' })
    expect(api().sort.value).toBeNull()
  })

  it('does not write a column that is not allowed', async () => {
    const { router, api } = await mountWith('/folios')
    api().setSort({ key: 'secret', dir: 'asc' })
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({})
  })
})
