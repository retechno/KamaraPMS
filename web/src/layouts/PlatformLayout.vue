<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, RouterView, useRoute } from 'vue-router'
import { t } from '@/i18n'
import { usePlatformAuthStore } from '@/stores/platformAuth'

/**
 * The frame of the platform console (docs/architecture/39-platform-console.md 11, design 78): a dark top bar with the mark, the Platform pill, the three places of the
 * console and the operator, and the content at most 1200 px wide. It has no hotel menu, property or business day: an operator belongs to no tenant.
 */
const auth = usePlatformAuthStore()
const route = useRoute()
const emit = defineEmits<{ signOut: [] }>()

const items = computed(() => [
  { to: '/platform/tenants', label: t('platform.nav.tenants'), active: route.path.startsWith('/platform/tenants') || route.path === '/platform' },
  { to: '/platform/plans', label: t('platform.nav.plans'), active: route.path.startsWith('/platform/plans') },
  { to: '/platform/invoices', label: t('platform.nav.invoices'), active: route.path.startsWith('/platform/invoices') },
])
</script>

<template>
  <div class="min-h-screen bg-background text-foreground" data-testid="platform-layout">
    <a href="#platform-main" class="sr-only focus:not-sr-only focus:absolute focus:left-2 focus:top-2 focus:z-50 focus:rounded-md focus:bg-card focus:px-3 focus:py-2">{{ t('platform.skip') }}</a>
    <header class="bg-[#1c2128] text-[#e6e9ee] dark:border-b dark:border-border dark:bg-card dark:text-foreground">
      <div class="mx-auto flex min-h-14 max-w-[1200px] flex-wrap items-center gap-x-5 px-4 sm:px-6">
        <RouterLink to="/platform/tenants" class="flex items-center gap-2.5 font-semibold text-white no-underline dark:text-foreground">
          <span aria-hidden="true" class="grid size-7 place-items-center rounded-[7px] bg-[#3fa98d] text-sm font-bold text-[#0c1512]">K</span>
          KamaraPMS
          <span class="rounded-full bg-[#e0a93a] px-2 py-0.5 text-xs uppercase tracking-wide text-[#0c1512]" data-testid="platform-pill">{{ t('platform.pill') }}</span>
        </RouterLink>
        <nav :aria-label="t('platform.nav.label')" class="flex flex-wrap gap-1">
          <RouterLink
            v-for="i in items"
            :key="i.to"
            :to="i.to"
            :aria-current="i.active ? 'page' : undefined"
            class="inline-flex min-h-11 items-center border-b-2 px-2.5 text-sm no-underline"
            :class="i.active ? 'border-[#3fa98d] font-semibold text-white dark:text-foreground' : 'border-transparent text-[#b8c0cb] hover:text-white dark:text-muted-foreground dark:hover:text-foreground'"
          >{{ i.label }}</RouterLink>
        </nav>
        <div class="ml-auto flex items-center gap-3 py-2 text-sm">
          <span class="text-[#b8c0cb] dark:text-muted-foreground" data-testid="platform-operator">{{ t('platform.operator', { name: auth.displayName }) }}</span>
          <button
            type="button"
            class="inline-flex h-8 cursor-pointer items-center rounded-md border border-[#3a414b] bg-transparent px-3 text-sm text-[#e6e9ee] hover:bg-white/10 pointer-coarse:min-h-[44px] dark:border-border dark:text-foreground"
            data-testid="platform-sign-out"
            @click="emit('signOut')"
          >{{ t('platform.signOut') }}</button>
        </div>
      </div>
    </header>
    <main id="platform-main" class="mx-auto flex max-w-[1200px] flex-col gap-5 px-4 pb-12 pt-7 sm:px-6">
      <RouterView />
    </main>
  </div>
</template>
