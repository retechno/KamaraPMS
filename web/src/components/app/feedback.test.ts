import { ApiError } from '@/api/problem'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { confirm } from '@/composables/useConfirm'
import { toast } from '@/composables/useToast'
import { setLocale } from '@/i18n'
import ConfirmDialog from './ConfirmDialog.vue'
import ConfirmHost from './ConfirmHost.vue'
import ToastHost from './ToastHost.vue'

let wrapper: VueWrapper | null = null
const $ = <T extends Element>(sel: string) => document.body.querySelector<T>(sel)
const click = async (sel: string) => {
  $<HTMLElement>(sel)!.click()
  await flushPromises()
}

afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  document.body.innerHTML = ''
  toast.clear()
  vi.useRealTimers()
  vi.restoreAllMocks()
})

beforeEach(() => setLocale('en'))

describe('confirm() with the host', () => {
  const host = async () => {
    wrapper = mount(ConfirmHost, { attachTo: document.body })
    await flushPromises()
  }

  it('resolves true when the person confirms', async () => {
    await host()
    const answer = confirm({ title: 'Delete the rule?', description: 'It cannot be undone.' })
    await flushPromises()
    expect($('[data-testid=confirm-dialog]')!.textContent).toContain('Delete the rule?')
    expect($('[data-testid=confirm-dialog]')!.textContent).toContain('It cannot be undone.')
    await click('[data-testid=confirm-ok]')
    await expect(answer).resolves.toBe(true)
    expect($('[data-testid=confirm-dialog]')).toBeNull()
  })

  it('resolves false on cancel and on Escape', async () => {
    await host()
    const a = confirm({ title: 'One?' })
    await flushPromises()
    await click('[data-testid=confirm-cancel]')
    await expect(a).resolves.toBe(false)

    const b = confirm({ title: 'Two?' })
    await flushPromises()
    $('[data-testid=confirm-dialog]')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await flushPromises()
    await expect(b).resolves.toBe(false)
  })

  it('uses its own button labels and styles a destructive confirm', async () => {
    await host()
    const answer = confirm({ title: 'Close the period?', confirmLabel: 'Close it', cancelLabel: 'Keep open', destructive: true })
    await flushPromises()
    expect($('[data-testid=confirm-ok]')!.textContent).toContain('Close it')
    expect($('[data-testid=confirm-ok]')!.className).toContain('bg-destructive')
    expect($('[data-testid=confirm-cancel]')!.textContent).toContain('Keep open')
    await click('[data-testid=confirm-cancel]')
    await answer
  })

  it('says no to a question that a newer one replaces', async () => {
    await host()
    const first = confirm({ title: 'First?' })
    const second = confirm({ title: 'Second?' })
    await flushPromises()
    await expect(first).resolves.toBe(false)
    expect($('[data-testid=confirm-dialog]')!.textContent).toContain('Second?')
    await click('[data-testid=confirm-ok]')
    await expect(second).resolves.toBe(true)
  })

  it('has the default words in the language of the person', async () => {
    setLocale('id')
    await host()
    const answer = confirm({ title: 'Hapus?' })
    await flushPromises()
    expect($('[data-testid=confirm-ok]')!.textContent).toContain('Konfirmasi')
    expect($('[data-testid=confirm-cancel]')!.textContent).toContain('Batal')
    await click('[data-testid=confirm-cancel]')
    await answer
  })
})

describe('confirm() without a host', () => {
  it('falls back to the browser dialog, so a page on its own still gets an answer', async () => {
    const spy = vi.spyOn(window, 'confirm').mockReturnValueOnce(true).mockReturnValueOnce(false)
    await expect(confirm({ title: 'Delete?', description: 'Really.' })).resolves.toBe(true)
    expect(spy).toHaveBeenCalledWith('Delete?\nReally.')
    await expect(confirm({ title: 'Again?' })).resolves.toBe(false)
  })

  it('stops answering through the host once it is gone', async () => {
    wrapper = mount(ConfirmHost, { attachTo: document.body })
    wrapper.unmount()
    wrapper = null
    const spy = vi.spyOn(window, 'confirm').mockReturnValue(true)
    await expect(confirm({ title: 'Delete?' })).resolves.toBe(true)
    expect(spy).toHaveBeenCalled()
  })
})

describe('ConfirmDialog on its own', () => {
  it('tells the page what was chosen and closes', async () => {
    wrapper = mount(ConfirmDialog, {
      props: { open: true, title: 'Void the payment?', 'onUpdate:open': (v: boolean) => void wrapper?.setProps({ open: v }) },
      attachTo: document.body,
    })
    await flushPromises()
    await click('[data-testid=confirm-ok]')
    expect(wrapper.emitted('confirm')).toHaveLength(1)
    expect(wrapper.emitted('cancel')).toBeUndefined()
    expect($('[data-testid=confirm-dialog]')).toBeNull()
  })

  it('reports a cancel', async () => {
    wrapper = mount(ConfirmDialog, {
      props: { open: true, title: 'Void?', 'onUpdate:open': (v: boolean) => void wrapper?.setProps({ open: v }) },
      attachTo: document.body,
    })
    await flushPromises()
    await click('[data-testid=confirm-cancel]')
    expect(wrapper.emitted('cancel')).toHaveLength(1)
    expect(wrapper.emitted('confirm')).toBeUndefined()
  })
})

describe('toasts', () => {
  it('shows a success, an info and an error with the right role', async () => {
    wrapper = mount(ToastHost)
    toast.success('Saved')
    toast.info('FYI')
    toast.error('It failed')
    await flushPromises()
    expect(wrapper.get('[data-testid=toast-success]').attributes('role')).toBe('status')
    expect(wrapper.get('[data-testid=toast-info]').text()).toBe('FYI')
    expect(wrapper.get('[data-testid=toast-error]').attributes('role')).toBe('alert')
    expect(wrapper.get('[data-testid=toast-error]').text()).toContain('It failed')
  })

  it('goes away by itself, a failure later than a success', async () => {
    vi.useFakeTimers()
    wrapper = mount(ToastHost)
    toast.success('Saved')
    toast.error('Failed')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(4100)
    expect(wrapper.find('[data-testid=toast-success]').exists()).toBe(false)
    expect(wrapper.find('[data-testid=toast-error]').exists()).toBe(true)
    await vi.advanceTimersByTimeAsync(6000)
    expect(wrapper.find('[data-testid=toast-error]').exists()).toBe(false)
  })

  it('does not stack the same message twice, and keeps the one on screen', async () => {
    wrapper = mount(ToastHost)
    toast.error('Same failure')
    toast.error('Same failure')
    await flushPromises()
    expect(wrapper.findAll('[data-testid=toast-error]')).toHaveLength(1)
  })

  it('holds a toast while the pointer is on it', async () => {
    vi.useFakeTimers()
    wrapper = mount(ToastHost)
    toast.error('Read me slowly')
    await flushPromises()
    await wrapper.get('[data-testid=toast-error]').trigger('mouseenter')
    await vi.advanceTimersByTimeAsync(60_000)
    expect(wrapper.find('[data-testid=toast-error]').exists()).toBe(true)
    await wrapper.get('[data-testid=toast-error]').trigger('mouseleave')
    await vi.advanceTimersByTimeAsync(4100)
    expect(wrapper.find('[data-testid=toast-error]').exists()).toBe(false)
  })

  it('turns an ApiError into its sentence, and anything else into a plain fallback', async () => {
    wrapper = mount(ToastHost)
    const api = toast.fromError(new ApiError({ status: 409, code: 'ROOM_TAKEN', title: 'Room taken' } as never))
    expect(api).toBeInstanceOf(ApiError)
    expect(toast.fromError(new Error('stack details'), 'Plain fallback')).toBeNull()
    await flushPromises()
    const text = wrapper.text()
    expect(text).toContain('Plain fallback')
    expect(text).not.toContain('stack details')
  })

  it('stays until closed when given no time, and closes with the button', async () => {
    vi.useFakeTimers()
    wrapper = mount(ToastHost)
    toast.info('Read me', 0)
    await flushPromises()
    await vi.advanceTimersByTimeAsync(60_000)
    expect(wrapper.find('[data-testid=toast-info]').exists()).toBe(true)
    await wrapper.get('[data-testid=toast-close]').trigger('click')
    expect(wrapper.find('[data-testid=toast-info]').exists()).toBe(false)
  })

  it('stacks the toasts in the order they came', async () => {
    wrapper = mount(ToastHost)
    toast.info('one', 0)
    toast.info('two', 0)
    await flushPromises()
    expect(wrapper.findAll('[data-testid=toast-info]').map((t) => t.text().trim())).toEqual(['one', 'two'])
  })
})
