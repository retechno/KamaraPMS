import { t } from '@/i18n'

/** The guests of a room in words, one way everywhere: "2 adults", "2 adults · 1 child" (in Indonesian "2 dewasa", "2 dewasa · 1 anak"). No children is not written. */
export function partyText(adults: number, children: number): string {
  return [t('reservations.paxAdults', { n: adults }, adults), ...(children > 0 ? [t('reservations.paxChildren', { n: children }, children)] : [])].join(' · ')
}
