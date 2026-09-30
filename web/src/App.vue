<script setup lang="ts">
import { computed, onUnmounted, watch } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import BusinessDateBar from './components/BusinessDateBar.vue'
import { navigation } from './navigation'
import { useAuthStore } from './stores/auth'
import { usePropertyStore } from './stores/property'
import { formatBusinessDate } from './utils/dates'

const auth = useAuthStore()
const property = usePropertyStore()
const route = useRoute()
const router = useRouter()
let timer: ReturnType<typeof setInterval> | undefined

const sections = computed(() =>
  navigation
    .map((s) => ({ ...s, items: s.items.filter((i) => !i.adminOnly || auth.isAdmin) }))
    .filter((s) => s.items.length > 0),
)

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
  <div v-else class="shell">
    <aside class="sidebar" aria-label="Main navigation">
      <div class="brand">
        <span class="brand-mark" aria-hidden="true">K</span>
        <span class="brand-name">KamaraPMS</span>
      </div>
      <nav>
        <section v-for="section in sections" :key="section.title" class="nav-section">
          <h2 class="nav-title">{{ section.title }}</h2>
          <ul>
            <li v-for="item in section.items" :key="item.label">
              <RouterLink v-if="item.to" :to="item.to" class="nav-item">{{ item.label }}</RouterLink>
              <span
                v-else
                class="nav-item is-disabled"
                :title="`Available from milestone ${item.milestone}`"
                aria-disabled="true"
              >
                {{ item.label }}
                <span class="badge">{{ item.milestone }}</span>
              </span>
            </li>
          </ul>
        </section>
      </nav>
    </aside>

    <div class="main">
      <header class="topbar">
        <BusinessDateBar />
        <div class="user">
          <RouterLink to="/account" class="user-name" data-testid="user-name">{{ auth.displayName }}</RouterLink>
          <button type="button" @click="signOut">Sign out</button>
        </div>
      </header>
      <main class="content">
        <p v-if="property.clock?.night_audit_overdue" class="alert warning" role="status" data-testid="overdue-banner">
          Night audit is overdue: the business date is still {{ formatBusinessDate(property.clock.business_date) }}.
          Postings go to that date until night audit closes it.
        </p>
        <RouterView />
      </main>
    </div>
  </div>
</template>

<style scoped>
.shell {
  display: grid;
  grid-template-columns: 248px 1fr;
  min-height: 100vh;
}
.sidebar {
  background: var(--surface-2);
  border-right: 1px solid var(--border);
  padding: 16px 12px;
  overflow-y: auto;
}
.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 4px 8px 16px;
}
.brand-mark {
  display: grid;
  place-items: center;
  width: 28px;
  height: 28px;
  border-radius: 8px;
  background: var(--accent);
  color: var(--accent-contrast);
  font-weight: 700;
}
.brand-name {
  font-weight: 650;
  letter-spacing: -0.01em;
}
.nav-section + .nav-section {
  margin-top: 14px;
}
.nav-title {
  margin: 0 0 4px;
  padding: 0 8px;
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--text-muted);
}
ul {
  list-style: none;
  margin: 0;
  padding: 0;
}
.nav-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 6px 8px;
  border-radius: 6px;
  color: var(--text);
  text-decoration: none;
  font-size: 14px;
}
a.nav-item:hover {
  background: var(--hover);
}
a.nav-item.router-link-exact-active {
  background: var(--accent-soft);
  color: var(--accent-strong);
  font-weight: 600;
}
.nav-item.is-disabled {
  color: var(--text-muted);
  cursor: default;
}
.badge {
  font-size: 10px;
  padding: 1px 6px;
  border-radius: 999px;
  border: 1px solid var(--border);
  color: var(--text-muted);
}
.main {
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.user {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 14px;
}
.user-name {
  color: var(--text);
  text-decoration: none;
  font-weight: 500;
}
.topbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  padding: 10px 24px;
  border-bottom: 1px solid var(--border);
  background: var(--surface);
}
.content {
  padding: 24px;
  max-width: 1200px;
  width: 100%;
}
@media (max-width: 760px) {
  .shell {
    grid-template-columns: 1fr;
  }
  .sidebar {
    border-right: none;
    border-bottom: 1px solid var(--border);
  }
  .content {
    padding: 16px;
  }
  .topbar {
    padding: 10px 16px;
  }
}
</style>
