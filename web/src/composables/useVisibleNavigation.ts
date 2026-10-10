import { computed } from 'vue'
import { type ActiveNav, type NavSection, visibleNavigation } from '@/navigation'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

/**
 * The menu this person sees at the open property: the pages for administrators only and the pages that name a permission they do not have are left out.
 * Everything that lists pages (the sidebar, the pinned and recent pages, the quick finder, the title of the top bar) reads it from here, so they agree.
 */
export function useVisibleNavigation() {
  const auth = useAuthStore()
  const property = usePropertyStore()

  const sections = computed<NavSection[]>(() => visibleNavigation(auth.isAdmin, (permission) => auth.can(permission, property.currentId)))
  const items = computed<ActiveNav[]>(() => sections.value.flatMap((section) => section.items.map((item) => ({ section, item }))))
  /** The visible item with this id; undefined when it is not in the menu, or not for this person. */
  const byId = (id: string): ActiveNav | undefined => items.value.find((x) => x.item.id === id)

  return { sections, items, byId }
}
