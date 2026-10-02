import { config } from '@vue/test-utils'
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
