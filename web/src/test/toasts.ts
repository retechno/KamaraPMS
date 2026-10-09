import { useToasts } from '@/composables/useToast'

/** The text of the toasts on screen, for a test of a page that raises them (the host in the shell is not mounted there). */
export function toastText(): string {
  return useToasts().items.value.map((x) => x.message).join(' | ')
}
