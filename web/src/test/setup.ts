import { config, type VueWrapper } from '@vue/test-utils'
import { afterEach } from 'vitest'
import { toast } from '@/composables/useToast'
import { i18n } from '@/i18n'
import { id } from '@/i18n/locales/id'
import { formatPlugin } from '@/utils/format'

// Every mounted component can use $t and useI18n without each test installing the plugin; tests run in English.
// Both languages are loaded up front, so a test can switch with setLocale() and read the result at once.
i18n.global.setLocaleMessage('id', id)
i18n.global.locale.value = 'en'
config.global.plugins = [i18n, formatPlugin]

// jsdom has no layout: the list of a combobox scrolls the highlighted item into view.
Element.prototype.scrollIntoView = () => {}

// A test that attaches its page to the document and does not unmount it leaves a live component behind: the next focus or timer updates it after its page is gone and the run fails on an unhandled
// error. Every root wrapper that is still in the document when its test ends is unmounted here. A page that is not attached, or that the test has already removed, is left alone.
const roots = new Set<VueWrapper>()
config.plugins.VueWrapper.install((wrapper) => {
  if ((wrapper as unknown as { __app?: unknown }).__app) roots.add(wrapper as VueWrapper)
  return {}
})
afterEach(() => {
  for (const w of roots) {
    try {
      if (w.element?.isConnected) w.unmount()
    } catch {
      // already gone
    }
  }
  roots.clear()
})

// The toasts are one list for the whole application: a test must not see the one of the test before.
afterEach(() => toast.clear())
