import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import type { Component } from 'vue'
import { setLocale } from '@/i18n'
import FilterBar from './FilterBar.vue'

let wrapper: VueWrapper | null = null
const wide = window.innerWidth
const setWidth = (w: number) => Object.defineProperty(window, 'innerWidth', { configurable: true, value: w })
const body = <T extends Element = HTMLElement>(sel: string): T | null => document.body.querySelector<T>(sel)

const slots = {
  search: '<input name="q" data-testid="search-field" />',
  default: '<select name="status" data-testid="status-field"><option value="">any</option></select><input name="from" type="date" data-testid="from-field" />',
  actions: '<button type="submit" data-testid="go">Search</button><button type="button" data-testid="clear">Clear</button>',
}

function mountBar(props: Record<string, unknown> = {}, over: Record<string, string> = {}, attrs: Record<string, unknown> = {}) {
  wrapper = mount(FilterBar as unknown as Component, { props, slots: { ...slots, ...over }, attrs, attachTo: document.body })
  return wrapper
}

describe('FilterBar', () => {
  beforeEach(() => setLocale('en'))
  afterEach(() => {
    wrapper?.unmount()
    wrapper = null
    document.body.innerHTML = ''
    setWidth(wide)
  })

  describe('on a tablet or a desktop', () => {
    it('is the form of fields the page had: the search, the filters and the buttons, with the page\'s own class, role and test id', () => {
      setWidth(1280)
      const w = mountBar({}, {}, { class: 'mb-4 flex gap-3', role: 'search', 'data-testid': 'filters' })
      const form = w.get('form')
      expect(form.classes()).toEqual(expect.arrayContaining(['mb-4', 'flex', 'gap-3']))
      expect(form.attributes('role')).toBe('search')
      expect(form.attributes('data-testid')).toBe('filters')
      expect(form.findAll('[data-testid]').map((e) => e.attributes('data-testid'))).toEqual(['search-field', 'status-field', 'from-field', 'go', 'clear'])
      expect(w.find('[data-testid=open-filters]').exists()).toBe(false)
    })

    it('says it is submitted', async () => {
      setWidth(1280)
      const w = mountBar()
      await w.get('form').trigger('submit')
      expect(w.emitted('submit')).toHaveLength(1)
    })

    it('can be a plain bar, not a form, for chips that are not submitted', () => {
      setWidth(1280)
      const w = mountBar({ plain: true })
      expect(w.find('form').exists()).toBe(false)
      expect(w.get('[data-slot=filter-bar]').element.tagName).toBe('DIV')
    })
  })

  describe('on a phone', () => {
    beforeEach(() => setWidth(390))

    it('is a short bar: the search field and a "Filter" button, with no other field in it', () => {
      const w = mountBar()
      const bar = w.get('[data-slot=filter-bar]')
      expect(bar.find('[data-testid=search-field]').exists()).toBe(true)
      expect(bar.find('[data-testid=status-field]').exists()).toBe(false)
      expect(bar.find('[data-testid=go]').exists()).toBe(false)
      expect(w.get('[data-testid=open-filters]').text()).toBe('Filter')
      expect(w.find('[data-testid=filter-count]').exists()).toBe(false) // nothing is on
    })

    it('counts the filters that are on in the button: "Filter (2)"', async () => {
      const w = mountBar({ active: 2 })
      expect(w.get('[data-testid=open-filters]').text()).toBe('Filter(2)')
      expect(w.get('[data-testid=filter-count]').text()).toBe('(2)')
      await w.setProps({ active: 0 })
      expect(w.find('[data-testid=filter-count]').exists()).toBe(false)
      await w.setProps({ active: 5 })
      expect(w.get('[data-testid=filter-count]').text()).toBe('(5)')
    })

    it('opens a sheet from the bottom with every filter and the buttons, and not before', async () => {
      const w = mountBar({ active: 1 })
      expect(body('[data-testid=filter-sheet]')).toBeNull()
      await w.get('[data-testid=open-filters]').trigger('click')
      await flushPromises()
      const sheet = body('[data-testid=filter-sheet]')!
      expect(sheet.className).toContain('bottom-0') // from the bottom edge
      expect(sheet.className).toContain('rounded-t-xl')
      expect(sheet.querySelector('[data-testid=status-field]')).not.toBeNull()
      expect(sheet.querySelector('[data-testid=from-field]')).not.toBeNull()
      expect(sheet.querySelector('[data-testid=go]')).not.toBeNull()
      expect(sheet.querySelector('[data-testid=clear]')).not.toBeNull()
      expect(sheet.textContent).toContain('Filters') // its title
      expect(sheet.querySelector('[data-testid=search-field]')).toBeNull() // the search stays in the bar
    })

    it('submits from the sheet, closes it and tells the page', async () => {
      const w = mountBar()
      await w.get('[data-testid=open-filters]').trigger('click')
      await flushPromises()
      body<HTMLFormElement>('[data-testid=filter-sheet-form]')!.dispatchEvent(new Event('submit', { cancelable: true }))
      await flushPromises()
      expect(w.emitted('submit')).toHaveLength(1)
      expect(body('[data-testid=filter-sheet]')).toBeNull()
    })

    it('submits from the search field of the bar too', async () => {
      const w = mountBar()
      await w.get('[data-slot=filter-bar]').trigger('submit')
      expect(w.emitted('submit')).toHaveLength(1)
    })

    it('has a "Done" button in the sheet when the page has no buttons of its own, and it closes the sheet', async () => {
      wrapper = mount(FilterBar as unknown as Component, { slots: { search: slots.search, default: slots.default }, attachTo: document.body })
      await wrapper.get('[data-testid=open-filters]').trigger('click')
      await flushPromises()
      expect(body('[data-testid=filters-done]')?.textContent).toBe('Done')
      body<HTMLButtonElement>('[data-testid=filters-done]')!.click()
      await flushPromises()
      expect(body('[data-testid=filter-sheet]')).toBeNull()
    })

    it('has no button when the list has only a search, and a full-width one when it has no search', async () => {
      const onlySearch = mount(FilterBar as unknown as Component, { slots: { search: slots.search }, attachTo: document.body })
      expect(onlySearch.find('[data-testid=open-filters]').exists()).toBe(false)
      expect(onlySearch.find('[data-testid=search-field]').exists()).toBe(true)
      onlySearch.unmount()
      const noSearch = mount(FilterBar as unknown as Component, { slots: { default: slots.default }, attachTo: document.body })
      expect(noSearch.get('[data-testid=open-filters]').classes()).toContain('w-full')
      noSearch.unmount()
    })

    it('does not give the form of a phone the class of the page, but keeps its role and test id', () => {
      const w = mountBar({}, {}, { class: 'grid gap-4 lg:grid-cols-4', role: 'search', 'data-testid': 'filters' })
      const bar = w.get('[data-slot=filter-bar]')
      expect(bar.classes()).not.toContain('lg:grid-cols-4')
      expect(bar.classes()).toContain('flex')
      expect(bar.attributes('role')).toBe('search')
      expect(bar.attributes('data-testid')).toBe('filters')
    })

    it('speaks Indonesian', async () => {
      setLocale('id')
      const w = mountBar({ active: 3 })
      expect(w.get('[data-testid=open-filters]').text()).toBe('Filter(3)')
      await w.get('[data-testid=open-filters]').trigger('click')
      await flushPromises()
      expect(body('[data-testid=filter-sheet]')?.textContent).toContain('Filter')
    })
  })
})
