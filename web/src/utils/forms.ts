import { focusProgrammatically } from '@/lib/focus'

/**
 * The last look before a form is sent: what the date fields (DateInput) show must be what is sent. A date field that is not valid (a text that is not
 * complete, a date that is not on the calendar, a date outside min and max) says so in its validity. When one is found, the submit is stopped, the field
 * tells why (the `invalid` event of `checkValidity`) and the cursor goes to the first one. Returns whether the form may go on.
 *
 * It looks at the date fields of the form of the event and of the elements in `also` (a form that sends what another form holds, like the booking of a
 * search). The other fields are left alone: the forms are `novalidate` and a refused number or a missing name is the business of the server, which gives its
 * reason in words; a field that the browser marks invalid without a message of its own would stop the form with no explanation. The native date field that
 * DateInput keeps hidden is never looked at (out of the tab order, it could not be corrected).
 *
 *     <form novalidate @submit.prevent="guardForm($event) && save()">
 */
export function guardForm(event: Event, ...also: (Element | null | undefined)[]): boolean {
  const own = event.currentTarget instanceof Element ? event.currentTarget : event.target instanceof Element ? event.target : null
  const roots = [own, ...also].filter((r): r is Element => !!r)
  const fields = roots.flatMap((root) => Array.from(root.querySelectorAll<HTMLInputElement>('input[data-slot="date-input-text"]')))
  const refused = fields.filter((field) => !field.checkValidity())
  if (refused.length === 0) return true
  event.preventDefault()
  event.stopImmediatePropagation()
  focusProgrammatically(refused[0]!)
  return false
}

/** Whether a date field (DateInput) inside `root` holds a text that is not valid: a search must not run while the screen shows a date that it would not use. */
export function hasInvalidDate(root: Element | null | undefined): boolean {
  if (!root) return false
  return Array.from(root.querySelectorAll<HTMLInputElement>('input[data-slot="date-input-text"]')).some((field) => !field.validity.valid)
}
