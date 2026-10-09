import type { Directive } from 'vue'
import { nextTick } from 'vue'

/** The fields a form can start in: not hidden, not disabled, not a button. */
const FIELD = 'input:not([type=hidden]):not([type=checkbox]):not([type=radio]):not([disabled]), select:not([disabled]), textarea:not([disabled]), [data-autofocus]:not([disabled])'

/**
 * `v-autofocus` puts the cursor where the person starts when a form appears. On a field it focuses that field; on a container (a form, a card) it focuses the field marked `data-autofocus`, else the
 * first field that is not disabled or hidden. It runs once, when the element is mounted, after the next render, so a form that is drawn only when it is opened (`v-if`) is there when it is asked
 * to focus. It does nothing when the person has already put the focus on a field inside it, and `v-autofocus="false"` turns it off. It changes no value and submits nothing.
 */
export const vAutofocus: Directive<HTMLElement, boolean | undefined> = {
  mounted(el, binding) {
    if (binding.value === false) return
    void nextTick(() => {
      if (!el.isConnected) return
      if (el.contains(document.activeElement) && document.activeElement !== el && document.activeElement?.matches(FIELD)) return
      const target = el.matches(FIELD) ? el : (el.querySelector<HTMLElement>('[data-autofocus]:not([disabled])') ?? el.querySelector<HTMLElement>(FIELD))
      target?.focus({ preventScroll: false })
    })
  },
}
