<script setup lang="ts">
import { ChevronDown, Eye, EyeOff, Pin, PinOff } from 'lucide-vue-next'
import { computed, reactive, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { useNavPins } from '@/composables/useNavPins'
import { useVisibleNavigation } from '@/composables/useVisibleNavigation'
import { activeNav, type NavItem } from '@/navigation'
import { cn } from '@/lib/utils'
import { t } from '@/i18n'

/**
 * The menu. At the top, the pages the person pinned and the pages they opened last; below, every section, collapsible, with the current page highlighted. In `rail`
 * mode only icons show (a button for the pinned and recent pages, then each section linking to its first page); the full menu is one tap away in the drawer.
 */
defineProps<{ rail?: boolean }>()
const emit = defineEmits<{ navigate: []; openMenu: [] }>()

const STORAGE_KEY = 'pms.nav.open'

const route = useRoute()
const { sections } = useVisibleNavigation()
const { pinned, recent, showRecent, togglePin, setShowRecent } = useNavPins()
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
const hasShortcuts = computed(() => pinned.value.length > 0 || recent.value.length > 0)

const linkClass = (item: NavItem): string =>
  cn('flex min-w-0 flex-1 items-center rounded-md border-l-2 border-transparent px-3 py-1.5 text-sm text-foreground no-underline hover:bg-accent', isActive(item) && 'border-primary bg-primary/10 font-medium text-primary')
const headingClass = 'px-2 pb-1 text-xs font-semibold uppercase tracking-wide text-muted-foreground'
</script>

<template>
  <nav :aria-label="t('shell.mainNavigation')" class="flex flex-col gap-1" data-testid="sidebar-nav">
    <!-- icon rail -->
    <template v-if="rail">
      <button
        v-if="hasShortcuts"
        type="button"
        :title="t('nav.pinsAndRecent')"
        :aria-label="t('nav.pinsAndRecent')"
        class="mx-auto flex size-10 cursor-pointer items-center justify-center rounded-md border-0 bg-transparent text-muted-foreground hover:bg-accent hover:text-foreground"
        data-testid="rail-pins"
        @click="emit('openMenu')"
      >
        <Pin class="size-5" aria-hidden="true" />
      </button>
      <RouterLink
        v-for="section in sections"
        :key="section.id"
        :to="section.items[0]!.to"
        :title="t(`nav.sections.${section.id}` as never)"
        :aria-label="t(`nav.sections.${section.id}` as never)"
        :data-testid="`rail-${section.id}`"
        :class="cn('mx-auto flex size-10 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground', active?.section.id === section.id && 'bg-primary/10 text-primary')"
        @click="emit('navigate')"
      >
        <component :is="section.icon" class="size-5" aria-hidden="true" />
      </RouterLink>
    </template>

    <!-- full menu -->
    <template v-else>
      <section v-if="pinned.length" class="mb-2" data-testid="section-pinned" aria-labelledby="nav-pinned-title">
        <p id="nav-pinned-title" :class="cn(headingClass, 'm-0')">{{ t('nav.pinned') }}</p>
        <ul class="m-0 list-none p-0">
          <li v-for="item in pinned" :key="item.item.id" class="group flex items-center">
            <RouterLink :to="item.item.to" :data-testid="`pinned-${item.item.id}`" :aria-current="isActive(item.item) ? 'page' : undefined" :class="linkClass(item.item)" @click="emit('navigate')">
              {{ label(item.item) }}
            </RouterLink>
            <button
              type="button"
              :aria-label="t('nav.unpin', { page: label(item.item) })"
              :title="t('nav.unpin', { page: label(item.item) })"
              class="mr-1 grid size-7 shrink-0 cursor-pointer place-items-center rounded-md border-0 bg-transparent text-muted-foreground opacity-0 hover:bg-accent hover:text-foreground focus-visible:opacity-100 group-hover:opacity-100"
              :data-testid="`unpin-${item.item.id}`"
              @click="togglePin(item.item.id)"
            >
              <PinOff class="size-3.5" aria-hidden="true" />
            </button>
          </li>
        </ul>
      </section>

      <section v-if="recent.length" class="mb-2" data-testid="section-recent" aria-labelledby="nav-recent-title">
        <div class="flex items-center">
          <p id="nav-recent-title" :class="cn(headingClass, 'm-0 flex-1')">{{ t('nav.recent') }}</p>
          <button
            type="button"
            :aria-label="showRecent ? t('nav.hideRecent') : t('nav.showRecent')"
            :title="showRecent ? t('nav.hideRecent') : t('nav.showRecent')"
            :aria-expanded="showRecent"
            class="mr-1 grid size-6 cursor-pointer place-items-center rounded-md border-0 bg-transparent text-muted-foreground hover:bg-accent hover:text-foreground"
            data-testid="toggle-recent"
            @click="setShowRecent(!showRecent)"
          >
            <Eye v-if="!showRecent" class="size-3.5" aria-hidden="true" />
            <EyeOff v-else class="size-3.5" aria-hidden="true" />
          </button>
        </div>
        <ul v-show="showRecent" class="m-0 list-none p-0">
          <li v-for="item in recent" :key="item.item.id">
            <RouterLink :to="item.item.to" :data-testid="`recent-${item.item.id}`" :aria-current="isActive(item.item) ? 'page' : undefined" :class="linkClass(item.item)" @click="emit('navigate')">
              {{ label(item.item) }}
            </RouterLink>
          </li>
        </ul>
      </section>

      <p v-if="hasShortcuts" :class="cn(headingClass, 'm-0 mt-1')" data-testid="all-menu">{{ t('nav.allMenu') }}</p>

      <section v-for="section in sections" :key="section.id" :data-testid="`section-${section.id}`" class="mb-1">
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
          <li v-for="item in section.items" :key="item.id" class="group flex items-center">
            <RouterLink :to="item.to" :data-testid="`nav-${item.id}`" :aria-current="isActive(item) ? 'page' : undefined" :class="cn(linkClass(item), 'ml-3')" @click="emit('navigate')">
              {{ label(item) }}
            </RouterLink>
          </li>
        </ul>
      </section>
    </template>
  </nav>
</template>
