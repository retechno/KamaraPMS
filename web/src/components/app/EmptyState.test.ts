import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import EmptyState from './EmptyState.vue'

describe('EmptyState', () => {
  it('shows its title and the reason, and no button when there is no action', () => {
    const w = mount(EmptyState, { props: { title: 'No suppliers.', description: 'Suppliers are who the hotel buys from.' } })
    expect(w.text()).toContain('No suppliers.')
    expect(w.text()).toContain('Suppliers are who the hotel buys from.')
    expect(w.find('[data-testid=empty-action]').exists()).toBe(false)
    expect(w.find('[data-slot=empty-action]').exists()).toBe(false)
  })

  it('has a button for its action, which emits "action"', async () => {
    const w = mount(EmptyState, { props: { title: 'No suppliers.', actionLabel: 'Add the first supplier' } })
    const button = w.get('button[data-testid=empty-action]')
    expect(button.text()).toBe('Add the first supplier')
    await button.trigger('click')
    expect(w.emitted('action')).toHaveLength(1)
  })

  it('is a link when the action goes to a page', async () => {
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
    await router.push('/')
    const w = mount(EmptyState, { props: { title: 'x', actionLabel: 'Back to the dashboard', actionTo: '/' }, global: { plugins: [router] } })
    const link = w.get('a[data-testid=empty-action]')
    expect(link.text()).toBe('Back to the dashboard')
    expect(link.attributes('href')).toBe('/')
    expect(w.find('button').exists()).toBe(false)
  })

  it('has the action of a filter that clears as an outline button, and the add button as the main one', () => {
    const clear = mount(EmptyState, { props: { title: 'x', actionLabel: 'Clear filters', actionVariant: 'outline' } })
    const add = mount(EmptyState, { props: { title: 'x', actionLabel: 'Add' } })
    expect(clear.get('button').classes().join(' ')).toContain('border-border')
    expect(add.get('button').classes().join(' ')).toContain('bg-primary')
  })

  it('has an empty label for a person who may not do it: no button', () => {
    const w = mount(EmptyState, { props: { title: 'x', actionLabel: '' } })
    expect(w.find('button').exists()).toBe(false)
  })
})
