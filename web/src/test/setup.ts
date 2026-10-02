import { config } from '@vue/test-utils'
import { i18n } from '@/i18n'
import { formatPlugin } from '@/utils/format'

// Every mounted component can use $t and useI18n without each test installing the plugin; tests run in English.
i18n.global.locale.value = 'en'
config.global.plugins = [i18n, formatPlugin]
