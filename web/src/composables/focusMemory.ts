import { nextTick } from 'vue'
import { focusProgrammatically } from '@/lib/focus'

/**
 * Where the focus was last, so that it can be given back after a failed save. A button that is disabled while a request runs loses the focus to the page (`body`); when the request fails the person
 * is left with no place, and a keyboard user has to tab in from the top. The place is remembered here (the last element that had the focus, one listener for the whole application) and given back
 * by `restoreFocus`, which does it only when the focus really is lost.
 */
let last: HTMLElement | null = null
let installed = false

export function trackFocus(): void {
  if (installed || typeof document === 'undefined') return
  installed = true
  document.addEventListener('focusin', (e) => {
    if (e.target instanceof HTMLElement && e.target !== document.body) last = e.target
  })
}

const inDialog = (el: Element | null) => el?.closest('[role=dialog]') ?? null

/**
 * After the next render (the button is enabled again by then), puts the focus back on the element that had it when the save was started, if:
 * - the focus is lost (on the page itself): a person who has moved to another element on purpose is left there;
 * - that element is still on the page and can take the focus (not disabled);
 * - it is in the same dialog as the notice's anchor (or both are outside any dialog), so a failure never pulls the focus out of a dialog or into one.
 * It changes no value and does not touch the toast.
 */
export async function restoreFocus(anchorOf: () => Element | null): Promise<void> {
  await nextTick()
  const active = document.activeElement
  if (active && active !== document.body) return
  const el = last
  if (!el || !el.isConnected || (el as HTMLButtonElement).disabled || el.getAttribute('aria-hidden') === 'true') return
  if (inDialog(el) !== inDialog(anchorOf())) return
  focusProgrammatically(el)
}
