<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import ConfirmHost from '@/components/app/ConfirmHost.vue'
import ToastHost from '@/components/app/ToastHost.vue'
import CommandPalette from '@/components/shell/CommandPalette.vue'
import PropertySwitcher from '@/components/shell/PropertySwitcher.vue'
import SidebarNav from '@/components/shell/SidebarNav.vue'
import TopBar from '@/components/shell/TopBar.vue'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { useLayoutMode } from '@/composables/useLayoutMode'
import { useNavPins } from '@/composables/useNavPins'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { formatBusinessDate } from '@/utils/dates'

/**
 * The frame around every signed-in page: the menu (a full sidebar, an icon rail on a tablet, a drawer on a phone), the
 * top bar, the quick finder (Ctrl+K) and the content. Pages render in the default slot.
 */
const emit = defineEmits<{ signOut: [] }>()

const property = usePropertyStore()
const route = useRoute()
const { mode, collapsed, canCollapse, toggleCollapsed } = useLayoutMode()
const pins = useNavPins()

const drawer = ref(false)
const palette = ref(false)

// The page the person is on is the first of the pages opened last.
watch(() => route.path, (path) => pins.track(path), { immediate: true })

// Going to another page closes the drawer; leaving the drawer layout (a rotated tablet) closes it too.
watch(() => route.fullPath, () => (drawer.value = false))
watch(mode, (m) => {
  if (m === 'full') drawer.value = false
})

function onKeydown(event: KeyboardEvent): void {
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k') {
    event.preventDefault()
    palette.value = !palette.value
  }
}
onMounted(() => window.addEventListener('keydown', onKeydown))
onUnmounted(() => window.removeEventListener('keydown', onKeydown))
</script>

<template>
  <div class="flex min-h-screen bg-background text-foreground">
    <a href="#main" class="sr-only focus:not-sr-only focus:fixed focus:left-2 focus:top-2 focus:z-50 focus:rounded-md focus:bg-card focus:px-3 focus:py-2">
      {{ t('shell.skipToContent') }}
    </a>

    <aside
      v-if="mode !== 'drawer'"
      :class="['sticky top-0 flex h-screen shrink-0 flex-col overflow-y-auto border-r border-border bg-muted px-2 py-3', mode === 'rail' ? 'w-16' : 'w-64']"
      :data-mode="mode"
      data-testid="sidebar"
    >
      <div :class="['flex items-center gap-2.5 pb-4', mode === 'rail' ? 'justify-center' : 'px-2']">
        <span class="grid size-7 shrink-0 place-items-center rounded-lg bg-primary font-bold text-primary-foreground" aria-hidden="true">K</span>
        <span v-if="mode === 'full'" class="font-semibold tracking-tight">KamaraPMS</span>
      </div>
      <SidebarNav :rail="mode === 'rail'" @open-menu="drawer = true" />
    </aside>

    <div class="flex min-w-0 flex-1 flex-col">
      <TopBar :mode="mode" :collapsed="collapsed" :can-collapse="canCollapse" @menu="drawer = true" @search="palette = true" @sign-out="emit('signOut')" @toggle-sidebar="toggleCollapsed" />
      <main id="main" class="flex-1 p-4 md:p-6" tabindex="-1">
        <p v-if="property.clock?.night_audit_overdue" class="alert warning" role="status" data-testid="overdue-banner">
          {{ t('shell.nightAuditBanner', { date: formatBusinessDate(property.clock.business_date) }) }}
        </p>
        <slot />
      </main>
    </div>

    <!-- the full menu as a drawer: on a phone, and from the icon rail on a tablet -->
    <Dialog v-model:open="drawer">
      <DialogContent variant="left" class="overflow-y-auto bg-muted p-3" data-testid="drawer">
        <DialogTitle class="sr-only">{{ t('shell.mainNavigation') }}</DialogTitle>
        <DialogDescription class="sr-only">{{ t('shell.mainNavigation') }}</DialogDescription>
        <div class="flex items-center gap-2.5 px-2 pb-4">
          <span class="grid size-7 place-items-center rounded-lg bg-primary font-bold text-primary-foreground" aria-hidden="true">K</span>
          <span class="font-semibold tracking-tight">KamaraPMS</span>
        </div>
        <!-- on a phone the top bar has no room for the property: it is chosen here -->
        <PropertySwitcher v-if="mode === 'drawer'" class="mb-3" />
        <SidebarNav @navigate="drawer = false" />
      </DialogContent>
    </Dialog>

    <CommandPalette v-model:open="palette" />
    <ConfirmHost />
    <ToastHost />
  </div>
</template>
