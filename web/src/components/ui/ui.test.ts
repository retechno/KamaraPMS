import { mount } from '@vue/test-utils'
import { BedDouble } from 'lucide-vue-next'
import { describe, expect, it } from 'vitest'
import { defineComponent, h } from 'vue'
import { cn } from '@/lib/utils'
import { Badge } from './badge'
import { Button } from './button'

describe('ui foundation', () => {
  it('cn lets the last utility win', () => {
    expect(cn('px-2 py-1', 'px-4')).toBe('py-1 px-4')
    expect(cn('a', false && 'b', undefined, 'c')).toBe('a c')
  })

  it('renders a button with its variant and size', () => {
    const w = mount(Button, { props: { variant: 'outline', size: 'sm' }, slots: { default: 'Save' } })
    expect(w.element.tagName).toBe('BUTTON')
    expect(w.text()).toBe('Save')
    expect(w.classes()).toContain('h-8')
    expect(w.classes()).toContain('border')
    expect(w.attributes('data-slot')).toBe('button')
  })

  it('lets a caller override a button class and keeps it disableable', () => {
    const w = mount(Button, { props: { class: 'h-12' }, attrs: { disabled: true, 'data-testid': 'go' }, slots: { default: 'Go' } })
    expect(w.classes()).toContain('h-12')
    expect(w.classes()).not.toContain('h-9')
    expect(w.attributes('disabled')).toBeDefined()
    expect(w.attributes('data-testid')).toBe('go')
  })

  it('renders an icon inside a button', () => {
    const w = mount(Button, { slots: { default: () => [h(BedDouble, { 'data-testid': 'icon' }), 'Rooms'] } })
    expect(w.find('[data-testid=icon]').exists()).toBe(true)
    expect(w.find('svg').exists()).toBe(true)
  })

  it('renders a badge variant', () => {
    const w = mount(Badge, { props: { variant: 'success' }, slots: { default: 'Open' } })
    expect(w.text()).toBe('Open')
    expect(w.classes().join(' ')).toContain('text-success')
  })

  it('gives every mounted component $t without installing the plugin in the test', () => {
    const C = defineComponent({ template: '<button>{{ $t("common.cancel") }}</button>' })
    expect(mount(C).text()).toBe('Cancel')
  })
})
