import '@fontsource-variable/plus-jakarta-sans'
import { createPinia } from 'pinia'
import { createApp } from 'vue'
import App from './App.vue'
import { i18n, initLocale } from './i18n'
import { initTheme } from './composables/useTheme'
import { router } from './router'
import { formatPlugin } from './utils/format'
import './assets/tailwind.css'

import { onPlatformSessionLost } from './api/platformSession'
import { onSessionLost } from './api/session'
import { useAuthStore } from './stores/auth'
import { usePlatformAuthStore } from './stores/platformAuth'

initTheme()
const pinia = createPinia()
// The language chosen last time is downloaded first, so the first page is drawn in it.
void initLocale().then(() => createApp(App).use(pinia).use(router).use(i18n).use(formatPlugin).mount('#app'))

// The server ended the session (logout elsewhere, deactivation, expiry): back to sign-in.
onSessionLost(() => {
  useAuthStore(pinia).signedOut()
  if (router.currentRoute.value.name !== 'login') {
    void router.push({ name: 'login', query: { redirect: router.currentRoute.value.fullPath } })
  }
})

// The same for the platform console, with its own session: back to its sign-in page, never the hotel's.
onPlatformSessionLost(() => {
  usePlatformAuthStore(pinia).signedOut()
  if (router.currentRoute.value.name !== 'platform-login') {
    void router.push({ name: 'platform-login', query: { redirect: router.currentRoute.value.fullPath } })
  }
})
