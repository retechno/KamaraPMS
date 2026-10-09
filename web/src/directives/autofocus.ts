import type { Directive } from 'vue'
import { nextTick } from 'vue'

/** The fields a form can start in: not hidden, not disabled, not a button. */
const FIELD = 'input:not([type=hidden]):not([type=checkbox]):not([type=radio]):not([disabled]), select:not([disabled]), textarea:not([disabled]), [data-autofocus]:not([disabled])'

function focusStart(el: HTMLElement): void {
  void nextTick(() => {
    if (!el.isConnected) return
    if (el.contains(document.activeElement) && document.activeElement !== el && document.activeElement?.matches(FIELD)) return
    const target = el.matches(FIELD) ? el : (el.querySelector<HTMLElement>('[data-autofocus]:not([disabled])') ?? el.querySelector<HTMLElement>(FIELD))
    target?.focus({ preventScroll: false })
  })
}

/**
 * `v-autofocus` puts the cursor where the person starts when a form appears. On a field it focuses that field; on a container (a form, a card) it focuses the field marked `data-autofocus`, else the
 * first field that is not disabled or hidden. It runs when the element is mounted, after the next render, so a form that is drawn only when it is opened (`v-if`) is there when it is asked
 * to focus. It does nothing when the person has already put the focus on a field inside it, and `v-autofocus="false"` turns it off. It changes no value and submits nothing.
 */
/**
 * A form that stays on the page when it is asked for again (the person presses the button that opens it while it is open, or the one that opens it for another subject) is not mounted again, so it
 * is told with a value: `v-autofocus="tick"`, where `tick` is a number that the opening button increases. Each change of the value focuses the start of the form again.
 */
export const vAutofocus: Directive<HTMLElement, boolean | number | undefined> = {
  mounted(el, binding) {
    if (binding.value === false) return
    focusStart(el)
  },
  updated(el, binding) {
    if (binding.value === false || binding.value === binding.oldValue || binding.value === undefined) return
    focusStart(el)
  },
}
