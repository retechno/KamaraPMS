<script setup lang="ts">
import { LogOut, Menu, PanelLeftClose, PanelLeftOpen, Search } from 'lucide-vue-next'
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { i18n, LOCALES, setLocale, t, type Locale } from '@/i18n'
import type { LayoutMode } from '@/composables/useLayoutMode'
import { activeNav, visibleNavigation } from '@/navigation'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatBusinessDate, wallClock } from '@/utils/dates'

const props = defineProps<{ mode: LayoutMode; collapsed: boolean; canCollapse: boolean }>()
const emit = defineEmits<{ menu: []; search: []; signOut: []; toggleSidebar: [] }>()

const auth = useAuthStore()
const property = usePropertyStore()
const route = useRoute()

const locale = computed(() => i18n.global.locale.value)

// "Section / Page" of the current route; a page outside the menu (a detail page) shows the title of its route.
const crumbs = computed(() => {
  const hit = activeNav(route.path, visibleNavigation(auth.isAdmin))
  if (!hit) return [String(route.meta.title ?? '')].filter(Boolean)
  return [t(`nav.sections.${hit.section.id}` as never), t(`nav.items.${hit.item.id}` as never)]
})

const local = computed(() => (property.clock ? wallClock(property.clock.property_local_time) : null))
const clockTitle = computed(() => {
  const c = property.clock
  if (!c || !local.value) return ''
  return `${formatBusinessDate(local.value.date)} ${local.value.time} (${c.timezone}). Server time ${c.server_time}.`
})

const audit = computed(() => {
  const c = property.clock
  if (!c) return null
  if (c.night_audit_overdue) return { variant: 'warning' as const, text: t('shell.nightAuditOverdue') }
  if (c.night_audit_allowed) return { variant: 'success' as const, text: t('shell.nightAuditReady') }
  return { variant: 'outline' as const, text: t('shell.nightAuditNotYet') }
})

function onSelectProperty(event: Event): void {
  const id = Number((event.target as HTMLSelectElement).value)
  if (id) void property.select(id)
}

function onSelectLocale(event: Event): void {
  setLocale((event.target as HTMLSelectElement).value as Locale)
}

const selectClass =
  'h-8 max-w-56 rounded-md border border-border bg-card px-2 text-sm text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring'
</script>

<template>
  <header class="sticky top-0 z-30 flex min-h-14 flex-wrap items-center gap-x-3 gap-y-2 border-b border-border bg-card px-4 py-2" data-testid="topbar">
    <Button v-if="!props.canCollapse" variant="ghost" size="icon" :aria-label="t('shell.openMenu')" data-testid="open-menu" @click="emit('menu')">
      <Menu />
    </Button>
    <Button
      v-else
      variant="ghost"
      size="icon"
      :aria-label="props.collapsed ? t('shell.expandMenu') : t('shell.collapseMenu')"
      data-testid="toggle-sidebar"
      @click="emit('toggleSidebar')"
    >
      <PanelLeftOpen v-if="props.collapsed" />
      <PanelLeftClose v-else />
    </Button>

    <nav v-if="crumbs.length" aria-label="Breadcrumb" class="min-w-0 truncate text-sm" data-testid="breadcrumb">
      <template v-for="(c, i) in crumbs" :key="i">
        <span v-if="i > 0" class="px-1 text-muted-foreground" aria-hidden="true">/</span>
        <span :class="i === crumbs.length - 1 ? 'font-semibold text-foreground' : 'text-muted-foreground'">{{ c }}</span>
      </template>
    </nav>

    <div class="ml-auto flex flex-wrap items-center gap-2">
      <template v-if="property.hasProperties">
        <label>
          <span class="sr-only">{{ t('shell.property') }}</span>
          <select :value="property.currentId ?? ''" :class="selectClass" data-testid="property-switcher" @change="onSelectProperty">
            <option v-for="p in property.properties" :key="p.id" :value="p.id">{{ p.code }} · {{ p.name }}</option>
          </select>
        </label>
        <span
          v-if="property.clock"
          class="inline-flex items-center gap-1 rounded-full border border-border bg-background px-3 py-1 text-sm"
          :class="property.clock.night_audit_overdue && 'border-warning'"
          :title="clockTitle"
          data-testid="business-date"
        >
          {{ t('shell.businessDate') }} <strong>{{ formatBusinessDate(property.clock.business_date) }}</strong>
          <span v-if="local && local.date !== property.clock.business_date" class="text-muted-foreground">· {{ t('shell.localTime') }} {{ formatBusinessDate(local.date) }} {{ local.time }}</span>
        </span>
        <RouterLink v-if="audit" to="/night-audit" class="no-underline" data-testid="night-audit-status">
          <Badge :variant="audit.variant">{{ audit.text }}</Badge>
        </RouterLink>
      </template>
      <RouterLink v-else-if="property.loaded" to="/setup/properties/new" class="rounded-full border border-border bg-background px-3 py-1 text-sm text-foreground no-underline">
        {{ t('shell.noPropertyYet') }}
      </RouterLink>

      <Button variant="outline" size="sm" :aria-label="t('shell.search')" data-testid="open-search" @click="emit('search')">
        <Search />
        <span class="hidden sm:inline">{{ t('shell.search') }}</span>
        <kbd class="hidden rounded border border-border px-1 text-[10px] text-muted-foreground lg:inline">Ctrl K</kbd>
      </Button>

      <label>
        <span class="sr-only">{{ t('language.label') }}</span>
        <select :value="locale" :class="selectClass" data-testid="language-switcher" @change="onSelectLocale">
          <option v-for="l in LOCALES" :key="l" :value="l">{{ t(`language.${l}` as never) }}</option>
        </select>
      </label>

      <RouterLink to="/account" class="text-sm text-foreground" data-testid="user-name">{{ auth.displayName }}</RouterLink>
      <Button variant="ghost" size="sm" data-testid="sign-out" @click="emit('signOut')">
        <LogOut />
        <span class="hidden sm:inline">{{ t('shell.signOut') }}</span>
      </Button>
    </div>
  </header>
</template>
