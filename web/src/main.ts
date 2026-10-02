import { createPinia } from 'pinia'
import { createApp } from 'vue'
import App from './App.vue'
import { currentLocale, i18n } from './i18n'
import { router } from './router'
import './assets/tailwind.css'

import { onSessionLost } from './api/session'
import { useAuthStore } from './stores/auth'

const pinia = createPinia()
document.documentElement.lang = currentLocale()
createApp(App).use(pinia).use(router).use(i18n).mount('#app')

// The server ended the session (logout elsewhere, deactivation, expiry): back to sign-in.
onSessionLost(() => {
  useAuthStore(pinia).signedOut()
  if (router.currentRoute.value.name !== 'login') {
    void router.push({ name: 'login', query: { redirect: router.currentRoute.value.fullPath } })
  }
})
