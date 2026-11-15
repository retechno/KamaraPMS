import { formatMoney } from '@/utils/format'

/**
 * An amount of the platform console as the hotel's Subscription page writes it: the code of the currency and the grouped amount ("IDR 1.500.000").
 * The server sends numeric(18,3) ("1500000.000"); whole zeros after the point are dropped, real decimals are kept.
 */
export function platformMoney(currency: string, amount: string | null | undefined): string {
  if (amount === null || amount === undefined || amount === '') return ''
  const trimmed = /^-?\d+\.\d+$/.test(amount) ? amount.replace(/\.?0+$/, '') : amount
  return `${currency} ${formatMoney(trimmed)}`
}
