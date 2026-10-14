import { afterEach, describe, expect, it } from 'vitest'
import { i18n, loadLocale, setLocale } from '@/i18n'
import { folioDescription } from './folioDescription'

describe('folioDescription', () => {
  afterEach(async () => {
    await setLocale('en')
  })

  it('leaves the stored English text as it is in English', () => {
    expect(folioDescription('Room 209 - 12 Oct 2026')).toBe('Room 209 - 12 Oct 2026')
    expect(folioDescription('Payment PAY000001 (BANK_TRANSFER)')).toBe('Payment PAY000001 (BANK_TRANSFER)')
  })

  it('translates the room night, the payment, the refund and the transfer on an Indonesian screen', async () => {
    await loadLocale('id')
    await setLocale('id')
    expect(i18n.global.locale.value).toBe('id')
    expect(folioDescription('Room 209 - 12 Oct 2026')).toBe('Kamar 209 - 12 Okt 2026')
    expect(folioDescription('Room 5 - 3 May 2026')).toBe('Kamar 5 - 3 Mei 2026')
    expect(folioDescription('Payment PAY000001 (BANK_TRANSFER)')).toBe('Pembayaran PAY000001 (Transfer bank)')
    expect(folioDescription('Refund PAY000002 (CASH)')).toBe('Pengembalian PAY000002 (Tunai)')
    expect(folioDescription('Transfer from FOL000009')).toBe('Transfer dari FOL000009')
    expect(folioDescription('Transfer to FOL000009')).toBe('Transfer ke FOL000009')
    expect(folioDescription('Reversal: Room 209 - 12 Oct 2026')).toBe('Pembalikan: Kamar 209 - 12 Okt 2026')
    expect(folioDescription('Adjustment: wrong rate')).toBe('Penyesuaian: wrong rate')
  })

  it('shows what people typed as it was posted', async () => {
    await loadLocale('id')
    await setLocale('id')
    expect(folioDescription('Minibar 1')).toBe('Minibar 1')
    expect(folioDescription('Payment by the company')).toBe('Payment by the company')
    expect(folioDescription('')).toBe('')
  })
})
