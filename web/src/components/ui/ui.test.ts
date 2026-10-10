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

  it('never underlines a button, so an <a> rendered as one does not inherit the link underline', () => {
    expect(mount(Button, { slots: { default: 'Go' } }).classes()).toContain('no-underline')
    expect(mount(Button, { props: { variant: 'link' }, slots: { default: 'Go' } }).classes()).toContain('hover:underline')
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

describe('ui: card, form controls, tabs and sheet', () => {
  it('renders a card with its parts', async () => {
    const { Card, CardContent, CardDescription, CardHeader, CardTitle } = await import('./card')
    const w = mount({
      components: { Card, CardContent, CardDescription, CardHeader, CardTitle },
      template: '<Card><CardHeader><CardTitle>Rooms</CardTitle><CardDescription>All</CardDescription></CardHeader><CardContent>body</CardContent></Card>',
    })
    expect(w.get('h2').text()).toBe('Rooms')
    expect(w.get('[data-slot=card]').classes()).toContain('rounded-xl')
    expect(w.text()).toContain('body')
  })

  it('binds an input and a select with v-model and passes attributes', async () => {
    const { Input } = await import('./input')
    const { NativeSelect } = await import('./native-select')
    const w = mount({
      components: { Input, NativeSelect },
      data: () => ({ name: 'a', kind: 'x' }),
      template: '<div><Input v-model="name" name="n" aria-invalid="true" /><NativeSelect v-model="kind" name="k"><option value="x">X</option><option value="y">Y</option></NativeSelect><i>{{ name }}{{ kind }}</i></div>',
    })
    await w.get('input[name=n]').setValue('bali')
    await w.get('select[name=k]').setValue('y')
    expect(w.get('i').text()).toBe('baliy')
    expect(w.get('input').attributes('aria-invalid')).toBe('true')
    expect(w.get('input').classes()).toContain('h-9')
  })

  it('switches tabs', async () => {
    const { Tabs, TabsContent, TabsList, TabsTrigger } = await import('./tabs')
    const w = mount({
      components: { Tabs, TabsContent, TabsList, TabsTrigger },
      template:
        '<Tabs default-value="a"><TabsList><TabsTrigger value="a" data-testid="ta">A</TabsTrigger><TabsTrigger value="b" data-testid="tb">B</TabsTrigger></TabsList><TabsContent value="a">first</TabsContent><TabsContent value="b">second</TabsContent></Tabs>',
    })
    expect(w.text()).toContain('first')
    expect(w.text()).not.toContain('second')
    await w.get('[data-testid=tb]').trigger('mousedown', { button: 0 })
    await w.get('[data-testid=tb]').trigger('focus')
    expect(w.text()).toContain('second')
    expect(w.get('[data-testid=tb]').attributes('data-state')).toBe('active')
  })

  it('draws a skeleton as decoration only', async () => {
    const { Skeleton } = await import('./skeleton')
    expect(mount(Skeleton).attributes('aria-hidden')).toBe('true')
  })

  it('opens a sheet from the right side', async () => {
    const { Sheet, SheetContent, SheetTitle } = await import('./sheet')
    const w = mount({
      components: { Sheet, SheetContent, SheetTitle },
      template: '<Sheet :open="true"><SheetContent data-testid="sheet"><SheetTitle>Detail</SheetTitle></SheetContent></Sheet>',
    }, { attachTo: document.body })
    await Promise.resolve()
    const el = document.body.querySelector('[data-testid=sheet]')!
    expect(el.className).toContain('right-0')
    expect(el.textContent).toContain('Detail')
    w.unmount()
    document.body.innerHTML = ''
  })
})
