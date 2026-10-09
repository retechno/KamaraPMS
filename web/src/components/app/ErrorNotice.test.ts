import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { ApiError } from '@/api/problem'
import { toastText } from '@/test/toasts'
import ErrorNotice from './ErrorNotice.vue'

const refusal = new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'ROOM_OCCUPIED', detail: 'the room is occupied' })
const invalid = new ApiError({ type: 't', title: 'Invalid', status: 422, code: 'VALIDATION_FAILED', detail: 'check the fields', errors: [{ field: 'reason', code: 'REQUIRED' }] } as never)

describe('ErrorNotice', () => {
  it('raises a business refusal as a toast and draws nothing on the page', async () => {
    const w = mount(ErrorNotice, { props: { error: refusal } })
    await flushPromises()
    expect(w.find('[role=alert]').exists()).toBe(false)
    expect(toastText()).toContain('the room is occupied')
  })

  it('keeps a validation refusal inline, beside the fields, with no toast', async () => {
    const w = mount(ErrorNotice, { props: { error: invalid }, attrs: { 'data-testid': 'my-error' } })
    await flushPromises()
    expect(w.get('[data-testid=my-error]').text()).toContain('check the fields')
    expect(toastText()).toBe('')
  })

  it('keeps any error inline when asked to', async () => {
    const w = mount(ErrorNotice, { props: { error: refusal, inline: true } })
    await flushPromises()
    expect(w.get('[role=alert]').text()).toContain('the room is occupied')
    expect(toastText()).toBe('')
  })

  it('raises one toast for one failure however often it is drawn, and none once cleared', async () => {
    const w = mount(ErrorNotice, { props: { error: refusal } })
    await w.setProps({ error: null })
    await w.setProps({ error: refusal })
    await flushPromises()
    expect(toastText().split('the room is occupied')).toHaveLength(2)
  })
})
