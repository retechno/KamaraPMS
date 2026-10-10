/**
 * Focus that the page gives by itself (a form that appears, a refused field, a failed save) and not the person. A field that opens a list when it gets the focus (the combobox) asks `isProgrammaticFocus`
 * so that it does not open the list over the form at that moment: the person did not ask for it. A click, a key or a Tab still open it.
 */
let programmatic = false

export const isProgrammaticFocus = (): boolean => programmatic

export function focusProgrammatically(el: HTMLElement, options?: FocusOptions): void {
  programmatic = true
  try {
    el.focus(options)
  } finally {
    programmatic = false
  }
}
