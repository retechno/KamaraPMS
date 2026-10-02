import { computed, onMounted, onUnmounted, ref } from 'vue'

/** How the shell lays out the menu: the full sidebar, an icon rail, or a drawer opened from the top bar. */
export type LayoutMode = 'full' | 'rail' | 'drawer'

const STORAGE_KEY = 'pms.sidebar.collapsed'

/** Widths where the layout changes: tablets in portrait get the rail, phones the drawer. */
export const BREAKPOINTS = { tablet: 768, desktop: 1024 } as const

function readCollapsed(): boolean {
  try {
    return localStorage.getItem(STORAGE_KEY) === '1'
  } catch {
    return false
  }
}

/**
 * The layout mode follows the window width; on a wide window the person can also collapse the sidebar to the rail, and
 * the choice is remembered on this browser.
 */
export function useLayoutMode() {
  const width = ref(typeof window === 'undefined' ? 1280 : window.innerWidth)
  const collapsed = ref(readCollapsed())
  const onResize = () => {
    width.value = window.innerWidth
  }
  onMounted(() => window.addEventListener('resize', onResize))
  onUnmounted(() => window.removeEventListener('resize', onResize))

  const mode = computed<LayoutMode>(() => {
    if (width.value < BREAKPOINTS.tablet) return 'drawer'
    if (width.value < BREAKPOINTS.desktop) return 'rail'
    return collapsed.value ? 'rail' : 'full'
  })

  function toggleCollapsed(): void {
    collapsed.value = !collapsed.value
    try {
      localStorage.setItem(STORAGE_KEY, collapsed.value ? '1' : '0')
    } catch {
      // storage unavailable: the choice lasts until the page is closed
    }
  }

  // Only a wide window can collapse the sidebar; a narrower one is a rail or a drawer whatever was chosen.
  const canCollapse = computed(() => width.value >= BREAKPOINTS.desktop)

  return { mode, collapsed, canCollapse, toggleCollapsed }
}
