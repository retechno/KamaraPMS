import { ref, shallowRef } from 'vue'
import type { Arrival } from '@/api/types'

/**
 * The check-in sheet of the shell, opened from places that are not the arrivals list (the panel of a room). It holds the arrival to check in; the lists that show arrivals watch `checkedInCount` to read
 * themselves again when one was done here.
 */
const target = shallowRef<Arrival | null>(null)
export const checkedInCount = ref(0)

export function openCheckIn(arrival: Arrival): void {
  target.value = arrival
}

export function closeCheckIn(): void {
  target.value = null
}

export function useCheckInSheet() {
  return { target, checkedInCount, open: openCheckIn, close: closeCheckIn }
}
