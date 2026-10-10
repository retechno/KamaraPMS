import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { defineComponent, h } from 'vue'

const Page = defineComponent({ setup: () => () => h('div', { 'data-testid': 'leftover' }, 'page') })

// The setup file unmounts a page that a test attached to the document and left there.
describe('test cleanup', () => {
  it('leaves an attached page mounted at the end of the test', () => {
    mount(Page, { attachTo: document.body })
    expect(document.querySelector('[data-testid=leftover]')).not.toBeNull()
  })

  it('finds the document empty in the next test', () => {
    expect(document.querySelector('[data-testid=leftover]')).toBeNull()
  })
})
