import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, describe, expect, it } from 'vitest'
import { defineComponent, h, ref } from 'vue'
import { ApiError } from '@/api/problem'
import ErrorNotice from './ErrorNotice.vue'

let wrapper: VueWrapper | undefined
afterEach(() => { wrapper?.unmount(); wrapper = undefined; document.body.innerHTML = '' })

const refusal = () => new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'ROOM_OCCUPIED', detail: 'the room is occupied' })

/** A form that saves like the real ones: the button is disabled while the request runs (so it loses the focus), then the failure is shown. */
function mountForm(inDialog = false) {
  const error = ref<ApiError | null>(null)
  const busy = ref(false)
  const value = ref('typed')
  const Form = defineComponent({
    setup: () => () => h('div', inDialog ? { role: 'dialog' } : {}, [
      h(ErrorNotice, { error: error.value }),
      h('form', { onSubmit: (e: Event) => e.preventDefault() }, [
        h('input', { name: 'a', value: value.value }),
        h('input', { name: 'b' }),
        h('button', { type: 'button', name: 'save', disabled: busy.value, onClick: () => { void fail() } }, 'Save'),
      ]),
    ]),
  })
  async function fail() {
    busy.value = true
    error.value = null
    await flushPromises()
    ;(document.activeElement as HTMLElement | null)?.blur() // the browser drops the focus of a button that becomes disabled
    await Promise.resolve()
    error.value = refusal()
    busy.value = false
  }
  wrapper = mount(Form, { attachTo: document.body })
  return { w: wrapper, error }
}
const name = () => (document.activeElement as HTMLInputElement | null)?.name

describe('focus after a failed save', () => {
  it('goes back to the Save button when the focus was lost', async () => {
    const { w } = mountForm()
    ;(w.get('button[name=save]').element as HTMLButtonElement).focus()
    await w.get('button[name=save]').trigger('click')
    await flushPromises()
    expect(name()).toBe('save')
  })

  it('goes back to the field the person was in, and changes no value', async () => {
    const { w } = mountForm()
    ;(w.get('input[name=a]').element as HTMLInputElement).focus()
    ;(w.get('button[name=save]').element as HTMLButtonElement).focus()
    ;(w.get('input[name=a]').element as HTMLInputElement).focus()
    await w.get('button[name=save]').trigger('click')
    await flushPromises()
    expect(name()).toBe('a')
    expect((w.get('input[name=a]').element as HTMLInputElement).value).toBe('typed')
  })

  it('works inside a dialog', async () => {
    const { w } = mountForm(true)
    ;(w.get('button[name=save]').element as HTMLButtonElement).focus()
    await w.get('button[name=save]').trigger('click')
    await flushPromises()
    expect(name()).toBe('save')
  })

  it('leaves the focus where the person put it on purpose', async () => {
    const { w } = mountForm()
    ;(w.get('button[name=save]').element as HTMLButtonElement).focus()
    const other = document.createElement('input')
    other.name = 'elsewhere'
    document.body.appendChild(other)
    const save = w.get('button[name=save]')
    await save.trigger('click')
    other.focus() // the person moved while the request ran
    await flushPromises()
    expect(name()).toBe('elsewhere')
  })
})
