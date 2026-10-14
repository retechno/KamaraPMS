import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { setLocale } from '@/i18n'
import MoneyInput from './MoneyInput.vue'

function mountInput(modelValue = '', decimals = 0) {
  return mount(MoneyInput, { props: { modelValue, decimals, 'onUpdate:modelValue': (v: string) => void w.setProps({ modelValue: v }) } })
}
let w: ReturnType<typeof mountInput>
const type = async (text: string) => {
  const el = w.get('input').element as HTMLInputElement
  el.value = text
  el.dispatchEvent(new Event('input', { bubbles: true }))
  await w.vm.$nextTick()
}

describe('MoneyInput', () => {
  it('groups the digits as they are typed and keeps a plain decimal text', async () => {
    setLocale('en')
    w = mountInput()
    await type('5000000')
    expect((w.get('input').element as HTMLInputElement).value).toBe('5,000,000')
    expect(w.props('modelValue')).toBe('5000000')
    await type('12abc')
    expect(w.props('modelValue')).toBe('12')
    await type('')
    expect(w.props('modelValue')).toBe('')
  })

  it('uses the separators of the language and the decimals of the currency', async () => {
    setLocale('id')
    w = mountInput('', 2)
    await type('1500000,505')
    expect(w.props('modelValue')).toBe('1500000.50') // two decimals at most
    expect((w.get('input').element as HTMLInputElement).value).toBe('1.500.000,50')
    setLocale('en')
  })

  it('takes no decimals for a currency with none', async () => {
    setLocale('en')
    w = mountInput('', 0)
    await type('150.5')
    expect(w.props('modelValue')).toBe('1505')
  })
})
