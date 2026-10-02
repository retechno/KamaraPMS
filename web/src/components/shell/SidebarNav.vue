<script setup lang="ts">
import { ChevronDown } from 'lucide-vue-next'
import { computed, reactive, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { activeNav, visibleNavigation, type NavItem } from '@/navigation'
import { useAuthStore } from '@/stores/auth'
import { cn } from '@/lib/utils'
import { t } from '@/i18n'

/**
 * The menu: collapsible sections with a highlighted current page. In `rail` mode only the section icons show (each links
 * to the first page of its section); the full menu is one tap away in the drawer.
 */
defineProps<{ rail?: boolean }>()
const emit = defineEmits<{ navigate: [] }>()

const STORAGE_KEY = 'pms.nav.open'

const auth = useAuthStore()
const route = useRoute()

const sections = computed(() => visibleNavigation(auth.isAdmin))
const active = computed(() => activeNav(route.path, sections.value))

function readOpen(): Record<string, boolean> {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    const v = raw ? (JSON.parse(raw) as unknown) : {}
    return typeof v === 'object' && v !== null ? (v as Record<string, boolean>) : {}
  } catch {
    return {}
  }
}

// What the person opened or closed. A section they never touched follows its default; the section of the current
// page is opened whenever they arrive in it.
const open = reactive<Record<string, boolean>>(readOpen())

function remember(): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(open))
  } catch {
    // storage unavailable: the state lasts until the page is closed
  }
}

const isOpen = (id: string, defaultOpen?: boolean): boolean => open[id] ?? defaultOpen ?? false

function toggle(id: string, defaultOpen?: boolean): void {
  open[id] = !isOpen(id, defaultOpen)
  remember()
}

watch(
  () => active.value?.section.id,
  (id) => {
    if (id && !isOpen(id, active.value?.section.defaultOpen)) open[id] = true
  },
  { immediate: true },
)

const label = (item: NavItem): string => t(`nav.items.${item.id}` as never)
const isActive = (item: NavItem): boolean => active.value?.item.id === item.id

/** A sub-heading shows where an item starts a new group inside its section. */
const startsGroup = (items: NavItem[], i: number): boolean => !!items[i]?.group && items[i]?.group !== items[i - 1]?.group
</script>

<template>
  <nav :aria-label="t('shell.mainNavigation')" class="flex flex-col gap-1" data-testid="sidebar-nav">
    <template v-for="section in sections" :key="section.id">
      <!-- icon rail -->
      <RouterLink
        v-if="rail"
        :to="section.items[0]!.to"
        :title="t(`nav.sections.${section.id}` as never)"
        :aria-label="t(`nav.sections.${section.id}` as never)"
        :data-testid="`rail-${section.id}`"
        :class="cn('mx-auto flex size-10 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground', active?.section.id === section.id && 'bg-primary/10 text-primary')"
        @click="emit('navigate')"
      >
        <component :is="section.icon" class="size-5" aria-hidden="true" />
      </RouterLink>

      <!-- full menu -->
      <section v-else :data-testid="`section-${section.id}`" class="mb-1">
        <button
          type="button"
          :aria-expanded="isOpen(section.id, section.defaultOpen)"
          :data-testid="`toggle-${section.id}`"
          class="flex w-full cursor-pointer items-center gap-2 rounded-md border-0 bg-transparent px-2 py-1.5 text-left text-xs font-semibold uppercase tracking-wide text-muted-foreground hover:bg-accent"
          @click="toggle(section.id, section.defaultOpen)"
        >
          <component :is="section.icon" class="size-4 shrink-0" aria-hidden="true" />
          <span class="flex-1">{{ t(`nav.sections.${section.id}` as never) }}</span>
          <ChevronDown :class="cn('size-4 shrink-0 transition-transform', !isOpen(section.id, section.defaultOpen) && '-rotate-90')" aria-hidden="true" />
        </button>
        <ul v-show="isOpen(section.id, section.defaultOpen)" class="m-0 mt-0.5 list-none p-0">
          <template v-for="(item, i) in section.items" :key="item.id">
            <li v-if="startsGroup(section.items, i)" class="px-2 pb-0.5 pt-2 pl-8 text-[11px] font-medium text-muted-foreground" :data-testid="`group-${item.group}`">
              {{ t(`nav.groups.${item.group}` as never) }}
            </li>
            <li>
              <RouterLink
                :to="item.to"
                :data-testid="`nav-${item.id}`"
                :aria-current="isActive(item) ? 'page' : undefined"
                :class="cn('ml-3 flex items-center rounded-md border-l-2 border-transparent px-3 py-1.5 text-sm text-foreground no-underline hover:bg-accent', isActive(item) && 'border-primary bg-primary/10 font-medium text-primary')"
                @click="emit('navigate')"
              >
                {{ label(item) }}
              </RouterLink>
            </li>
          </template>
        </ul>
      </section>
    </template>
  </nav>
</template>
