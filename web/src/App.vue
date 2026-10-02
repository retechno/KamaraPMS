<script setup lang="ts">
import { onUnmounted, watch } from 'vue'
import { RouterView, useRoute, useRouter } from 'vue-router'
import AppShell from './components/shell/AppShell.vue'
import { useAuthStore } from './stores/auth'
import { usePropertyStore } from './stores/property'

const auth = useAuthStore()
const property = usePropertyStore()
const route = useRoute()
const router = useRouter()
let timer: ReturnType<typeof setInterval> | undefined

// Property data needs an authenticated user; reload it whenever the user changes.
watch(
  () => auth.me?.user.id,
  (userId) => {
    clearInterval(timer)
    if (!userId) {
      property.reset()
      return
    }
    void property.loadProperties()
    // The business date only changes at night audit; the local clock is refreshed every minute.
    timer = setInterval(() => void property.refreshClock(), 60_000)
  },
  { immediate: true },
)
onUnmounted(() => clearInterval(timer))

async function signOut(): Promise<void> {
  await auth.logout()
  await router.push('/login')
}
</script>

<template>
  <RouterView v-if="route.meta.public" />
  <AppShell v-else @sign-out="signOut">
    <div class="w-full max-w-[1400px]">
      <RouterView />
    </div>
  </AppShell>
</template>
