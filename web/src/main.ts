import { createPinia } from 'pinia'
import { createApp } from 'vue'
import App from './App.vue'
import { i18n, initLocale } from './i18n'
import { router } from './router'
import { formatPlugin } from './utils/format'
import './assets/tailwind.css'

import { onSessionLost } from './api/session'
import { useAuthStore } from './stores/auth'

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
