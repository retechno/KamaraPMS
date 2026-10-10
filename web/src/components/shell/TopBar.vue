<script setup lang="ts">
import { Menu, PanelLeftClose, PanelLeftOpen, Search } from 'lucide-vue-next'
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import PropertySwitcher from '@/components/shell/PropertySwitcher.vue'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import type { LayoutMode } from '@/composables/useLayoutMode'
import { type ThemePreference, THEME_PREFERENCES, useTheme } from '@/composables/useTheme'
import { useVisibleNavigation } from '@/composables/useVisibleNavigation'
import { i18n, LOCALES, setLocale, t, type Locale } from '@/i18n'
import { cn } from '@/lib/utils'
import { activeNav } from '@/navigation'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatBusinessDate, wallClock } from '@/utils/dates'
import { finderShortcut } from '@/utils/platform'
import { routeTitle } from '@/utils/routeTitle'

/**
 * The top bar, one row. On a laptop or a desktop: the menu button, where the person is, the finder (wide, it opens the quick finder), the property, the business day with the
 * state of night audit, and the menu of the person (account, language, theme, sign out). On a phone: the menu button, the title of the page, then the finder as an icon, the
 * business day in short and the person; the choice of the property is in the menu drawer.
 */
const props = defineProps<{ mode: LayoutMode; collapsed: boolean; canCollapse: boolean }>()
const emit = defineEmits<{ menu: []; search: []; signOut: []; toggleSidebar: [] }>()

const auth = useAuthStore()
const property = usePropertyStore()
const route = useRoute()
const nav = useVisibleNavigation()
const theme = useTheme()

const phone = computed(() => props.mode === 'drawer')
const locale = computed(() => i18n.global.locale.value)
const shortcut = finderShortcut()

// "Section / Page" of the current route; a page outside the menu (a detail page) shows the title of its route.
const crumbs = computed(() => {
  const hit = activeNav(route.path, nav.sections.value)
  if (!hit) return [routeTitle(route)].filter(Boolean)
  return [t(`nav.sections.${hit.section.id}` as never), t(`nav.items.${hit.item.id}` as never)]
})

const local = computed(() => (property.clock ? wallClock(property.clock.property_local_time) : null))
const clockTitle = computed(() => {
  const c = property.clock
  if (!c || !local.value) return ''
  return `${formatBusinessDate(local.value.date)} ${local.value.time} (${c.timezone}). Server time ${c.server_time}.`
})
const businessDay = computed(() => (property.clock ? formatBusinessDate(property.clock.business_date) : ''))
const businessDayShort = computed(() => businessDay.value.replace(/ \d{4}$/, ''))

/** Overdue is always in words, in the colour of a warning, at every width; ready and later are a dot on a narrow bar and words from a wide one. */
const audit = computed(() => {
  const c = property.clock
  if (!c) return null
  if (c.night_audit_overdue) return { state: 'overdue' as const, text: t('shell.nightAuditOverdue'), short: t('shell.nightAuditOverdueShort'), dot: 'bg-warning' }
  if (c.night_audit_allowed) return { state: 'ready' as const, text: t('shell.nightAuditReady'), short: t('shell.nightAuditReady'), dot: 'bg-success' }
  return { state: 'later' as const, text: t('shell.nightAuditNotYet'), short: t('shell.nightAuditNotYet'), dot: 'bg-muted-foreground' }
})

const initials = computed(() => {
  const words = auth.displayName.trim().split(/\s+/).filter(Boolean)
  return ((words[0]?.[0] ?? '') + (words.length > 1 ? (words.at(-1)?.[0] ?? '') : '')).toUpperCase() || '?'
})

const themeLabel: Record<ThemePreference, string> = { light: 'account.themeLight', dark: 'account.themeDark', system: 'account.themeSystem' }
</script>

<template>
  <header class="sticky top-0 z-30 flex h-14 items-center gap-3 border-b border-border bg-card px-3 sm:px-4" data-testid="topbar">
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

    <!-- a phone: the title of the page -->
    <p v-if="phone" class="m-0 min-w-0 flex-1 truncate text-base font-semibold" data-testid="page-title">{{ crumbs.at(-1) }}</p>

    <!-- a laptop or a desktop: where the person is -->
    <nav v-else-if="crumbs.length" aria-label="Breadcrumb" class="hidden min-w-0 shrink truncate text-sm lg:block" data-testid="breadcrumb">
      <template v-for="(c, i) in crumbs" :key="i">
        <span v-if="i > 0" class="px-1 text-muted-foreground" aria-hidden="true">/</span>
        <span :class="i === crumbs.length - 1 ? 'font-semibold text-foreground' : 'text-muted-foreground'">{{ c }}</span>
      </template>
    </nav>

    <!-- the finder: a wide field that opens the quick finder; an icon on a phone -->
    <Button v-if="phone" variant="ghost" size="icon" :aria-label="t('shell.search')" data-testid="open-search" @click="emit('search')"><Search /></Button>
    <button
      v-else
      type="button"
      class="flex h-9 min-w-0 max-w-xl flex-1 cursor-pointer items-center gap-2 rounded-md border border-border bg-muted px-3 text-left text-sm text-muted-foreground hover:bg-accent"
      :aria-label="t('shell.search')"
      data-testid="open-search"
      @click="emit('search')"
    >
      <Search class="size-4 shrink-0" aria-hidden="true" />
      <span class="min-w-0 flex-1 truncate">{{ t('shell.searchPlaceholder') }}</span>
      <kbd class="hidden shrink-0 rounded border border-border bg-card px-1.5 text-[11px] text-muted-foreground sm:inline" data-testid="search-shortcut">{{ shortcut }}</kbd>
    </button>

    <div :class="cn('flex items-center gap-2', !phone && 'ml-auto')">
      <template v-if="property.hasProperties">
        <PropertySwitcher v-if="!phone" class="w-44 xl:w-52" />
        <RouterLink
          v-if="property.clock && audit"
          to="/night-audit"
          :class="
            cn(
              'inline-flex h-9 items-center gap-2 whitespace-nowrap rounded-md border px-2.5 text-sm no-underline',
              audit.state === 'overdue' ? 'border-status-noshow-line bg-status-noshow-bg text-status-noshow' : 'border-border bg-background text-foreground hover:bg-accent',
            )
          "
          :title="clockTitle"
          data-testid="business-date"
        >
          <span :class="cn('size-2 shrink-0 rounded-full', audit.dot)" aria-hidden="true" />
          <template v-if="phone">
            <strong>{{ businessDayShort }}</strong>
          </template>
          <template v-else>
            <span><span class="mr-1 hidden lg:inline">{{ t('shell.businessDate') }}</span><strong>{{ businessDay }}</strong></span>
            <span v-if="local && local.date !== property.clock.business_date" class="hidden text-muted-foreground xl:inline">· {{ t('shell.localTime') }} {{ formatBusinessDate(local.date) }} {{ local.time }}</span>
          </template>
          <span
            :class="cn('text-xs', audit.state === 'overdue' ? 'font-semibold' : 'sr-only xl:not-sr-only xl:text-muted-foreground')"
            :data-state="audit.state"
            data-testid="night-audit-status"
          >{{ phone ? audit.short : audit.text }}</span>
        </RouterLink>
      </template>
      <RouterLink v-else-if="property.loaded" to="/setup/properties/new" class="rounded-md border border-border bg-background px-3 py-1.5 text-sm text-foreground no-underline">
        {{ t('shell.noPropertyYet') }}
      </RouterLink>

      <DropdownMenu>
        <DropdownMenuTrigger as-child>
          <button
            type="button"
            class="grid size-9 shrink-0 cursor-pointer place-items-center rounded-full border-0 bg-foreground text-sm font-bold text-background focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
            :aria-label="t('shell.accountMenu')"
            :title="auth.displayName"
            data-testid="user-menu"
          >{{ initials }}</button>
        </DropdownMenuTrigger>
        <DropdownMenuContent class="w-64" data-testid="user-menu-content">
          <div class="px-2 py-1.5">
            <p class="m-0 truncate text-sm font-semibold" data-testid="user-name">{{ auth.displayName }}</p>
            <p class="m-0 truncate text-xs text-muted-foreground" data-testid="user-email">{{ auth.me?.user.email }}</p>
          </div>
          <DropdownMenuSeparator />
          <DropdownMenuItem as-child>
            <RouterLink to="/account" data-testid="account-link">{{ t('shell.myAccount') }}</RouterLink>
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuLabel>{{ t('language.label') }}</DropdownMenuLabel>
          <DropdownMenuRadioGroup :model-value="locale" data-testid="language-switcher" @update:model-value="(v) => setLocale(String(v) as Locale)">
            <DropdownMenuRadioItem v-for="l in LOCALES" :key="l" :value="l" :data-testid="`language-${l}`">{{ t(`language.${l}` as never) }}</DropdownMenuRadioItem>
          </DropdownMenuRadioGroup>
          <DropdownMenuSeparator />
          <DropdownMenuLabel>{{ t('shell.theme') }}</DropdownMenuLabel>
          <DropdownMenuRadioGroup :model-value="theme.preference.value" data-testid="theme-menu" @update:model-value="(v) => theme.setPreference(String(v) as ThemePreference)">
            <DropdownMenuRadioItem v-for="p in THEME_PREFERENCES" :key="p" :value="p" :data-testid="`menu-theme-${p}`">{{ t(themeLabel[p] as never) }}</DropdownMenuRadioItem>
          </DropdownMenuRadioGroup>
          <DropdownMenuSeparator />
          <DropdownMenuItem data-testid="sign-out" @select="emit('signOut')">{{ t('shell.signOut') }}</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  </header>
</template>
