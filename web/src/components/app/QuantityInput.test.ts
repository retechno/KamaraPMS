import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { setLocale } from '@/i18n'
import QuantityInput from './QuantityInput.vue'

function mountInput(modelValue = '', decimals?: number) {
  return mount(QuantityInput, { props: { modelValue, ...(decimals === undefined ? {} : { decimals }), 'onUpdate:modelValue': (v: string) => void w.setProps({ modelValue: v }) } })
}
let w: ReturnType<typeof mountInput>
const type = async (text: string) => {
  const el = w.get('input').element as HTMLInputElement
  el.value = text
  el.dispatchEvent(new Event('input', { bubbles: true }))
  await w.vm.$nextTick()
}

describe('QuantityInput', () => {
  it('takes the decimal separator of the language and keeps a plain decimal text', async () => {
    setLocale('id')
    w = mountInput()
    await type('12,5')
    expect((w.get('input').element as HTMLInputElement).value).toBe('12,5')
    expect(w.props('modelValue')).toBe('12.5')
    setLocale('en')
    w = mountInput()
    await type('12.5')
    expect((w.get('input').element as HTMLInputElement).value).toBe('12.5')
    expect(w.props('modelValue')).toBe('12.5')
  })

  it('has six decimals unless it is told otherwise', async () => {
    setLocale('en')
    w = mountInput()
    await type('1.1234567')
    expect(w.props('modelValue')).toBe('1.123456')
    w = mountInput('', 2)
    await type('1.999')
    expect(w.props('modelValue')).toBe('1.99')
    w = mountInput('', 0)
    await type('7.5')
    expect(w.props('modelValue')).toBe('75') // a unit counted in whole numbers has no separator
  })

  it('groups the digits as they are typed and passes its attributes on', async () => {
    setLocale('en')
    w = mount(QuantityInput, { props: { modelValue: '', 'onUpdate:modelValue': (v: string) => void w.setProps({ modelValue: v }) }, attrs: { name: 'qty_0', 'aria-label': 'Quantity' } })
    await type('1200.5')
    expect((w.get('input').element as HTMLInputElement).value).toBe('1,200.5')
    expect(w.get('input').attributes('name')).toBe('qty_0')
    expect(w.get('input').attributes('aria-label')).toBe('Quantity')
  })
})
