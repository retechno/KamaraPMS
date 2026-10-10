import { labelOf } from '@/i18n/labels'

/** The words for a status value (`CONFIRMED`) in the language of the page; a value without a label is shown as readable text (see `labelOf`). */
export function statusText(status: string | null | undefined): string {
  return labelOf('status', status)
}
