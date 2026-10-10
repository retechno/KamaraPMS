import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { setLocale } from '@/i18n'
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

  it('writes the next step for a code that has one, and folds the code and the request away under technical details', async () => {
    const withId = new ApiError({ type: 't', title: 'x', status: 409, code: 'ROOM_OCCUPIED', detail: 'the room is occupied', request_id: 'req-7' })
    const w = mount(ErrorNotice, { props: { error: withId, inline: true } })
    await flushPromises()
    expect(w.get('[data-slot=error-action]').text()).toBe('Choose another room.')
    const details = w.get('details[data-slot=error-details]')
    expect(details.get('summary').text()).toBe('Technical details')
    expect(details.text()).toContain('ROOM_OCCUPIED')
    expect(details.text()).toContain('Request req-7')
    expect(details.attributes('open')).toBeUndefined() // closed until asked for
    expect(w.get('p').text()).not.toContain('ROOM_OCCUPIED') // the message is for people
  })

  it('has no next step for a code that has none, and speaks the language of the page', async () => {
    const odd = new ApiError({ type: 't', title: 'x', status: 409, code: 'SOMETHING_RARE', detail: 'rare' })
    const w = mount(ErrorNotice, { props: { error: odd, inline: true } })
    await flushPromises()
    expect(w.find('[data-slot=error-action]').exists()).toBe(false)
    await setLocale('id')
    const w2 = mount(ErrorNotice, { props: { error: refusal, inline: true } })
    await flushPromises()
    expect(w2.get('[data-slot=error-action]').text()).toBe('Pilih kamar lain.')
    expect(w2.get('summary').text()).toBe('Rincian teknis')
    await setLocale('en')
  })

  it('shows what a view puts in it under the message', async () => {
    const w = mount(ErrorNotice, { props: { error: refusal, inline: true }, slots: { default: '<button data-testid="retry">Retry</button>' } })
    await flushPromises()
    expect(w.get('[data-testid=retry]').text()).toBe('Retry')
  })
})
