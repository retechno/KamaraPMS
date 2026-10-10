import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { setLocale } from '@/i18n'
import { drawerName, humanize, labelOf } from './labels'

describe('labelOf', () => {
  beforeEach(() => vi.spyOn(console, 'warn').mockImplementation(() => {}))
  afterEach(() => {
    vi.restoreAllMocks()
    return setLocale('en')
  })

  it('gives the words of a code in the language of the page', async () => {
    expect(labelOf('payMethod', 'BANK_TRANSFER')).toBe('Bank transfer')
    expect(labelOf('roomChargeStatus', 'ALREADY_POSTED')).toBe('Already posted')
    expect(labelOf('auditAction', 'payment.posted')).toBe('Payment posted')
    await setLocale('id')
    expect(labelOf('payMethod', 'BANK_TRANSFER')).toBe('Transfer bank')
    expect(labelOf('auditAction', 'payment.posted')).toBe('Pembayaran dicatat')
    expect(labelOf('status', 'CLEAN')).toBe('Bersih')
  })

  it('shows nothing for nothing', () => {
    expect(labelOf('status', null)).toBe('')
    expect(labelOf('status', undefined)).toBe('')
    expect(labelOf('status', '')).toBe('')
  })

  it('turns a code without a label into readable text and reports it once', () => {
    const warn = vi.mocked(console.warn)
    expect(labelOf('status', 'NEW_BACKEND_STATE')).toBe('New backend state')
    expect(labelOf('status', 'NEW_BACKEND_STATE')).toBe('New backend state')
    expect(labelOf('auditAction', 'thing.did_it')).toBe('Thing did it')
    expect(warn).toHaveBeenCalledTimes(2)
    expect(warn).toHaveBeenCalledWith('[i18n] no label for status.NEW_BACKEND_STATE')
    expect(warn).toHaveBeenCalledWith('[i18n] no label for auditAction.thing_did_it')
  })

  it('humanizes codes of every shape', () => {
    expect(humanize('ALREADY_POSTED')).toBe('Already posted')
    expect(humanize('payment.posted')).toBe('Payment posted')
    expect(humanize('room_type')).toBe('Room type')
  })

  it('names the usual drawer and leaves a name someone typed as it is', async () => {
    expect(drawerName('MAIN')).toBe('Main drawer')
    expect(drawerName('Front Counter 2')).toBe('Front Counter 2')
    await setLocale('id')
    expect(drawerName('MAIN')).toBe('Laci utama')
  })
})
