import { computed, inject, ref } from 'vue'
import { routeLocationKey } from 'vue-router'
import { activeNav, type ActiveNav } from '@/navigation'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { useVisibleNavigation } from './useVisibleNavigation'

/**
 * The pages a person pinned to the top of the menu, and the pages they opened last. They live in this browser (`localStorage`, one entry per user, so two people on one
 * computer have their own); keeping them on the server is for later. Only ids of menu items are kept. An id that is not in the menu any more, or is a page this person may not
 * open, is not shown and causes no error; it stays stored, so it comes back if the person gets the permission.
 */
export const RECENT_LIMIT = 5

interface Stored {
  pins?: string[]
  recent?: string[]
  showRecent?: boolean
}

/** What a person gets before they have pinned anything: the pages of every day, those they may open. */
export const DEFAULT_PINS: readonly { id: string; permission?: string }[] = [
  { id: 'dashboard' },
  { id: 'roomStatus', permission: 'reservation.read' },
  { id: 'tapeChart', permission: 'reservation.read' },
  { id: 'cashier', permission: 'folio.read' },
]

const storageKey = (userId: number | string | undefined): string => `pms.nav.${userId ?? 'anonymous'}`
const isIds = (v: unknown): v is string[] => Array.isArray(v) && v.every((x) => typeof x === 'string')

function read(key: string): Stored {
  try {
    const raw = localStorage.getItem(key)
    const v = raw ? (JSON.parse(raw) as Record<string, unknown>) : {}
    return {
      pins: isIds(v.pins) ? v.pins : undefined,
      recent: isIds(v.recent) ? v.recent : undefined,
      showRecent: typeof v.showRecent === 'boolean' ? v.showRecent : undefined,
    }
  } catch {
    return {}
  }
}

// What was changed in this session, by key. A key that was not changed is read from the storage.
const changed = ref<Record<string, Stored>>({})

export function useNavPins() {
  const auth = useAuthStore()
  const property = usePropertyStore()
  const route = inject(routeLocationKey, null) // no router in a page that is shown on its own (a test): then there is no page to track
  const nav = useVisibleNavigation()

  const key = computed(() => storageKey(auth.me?.user.id))
  const stored = computed<Stored>(() => changed.value[key.value] ?? read(key.value))

  function update(change: (s: Stored) => void): void {
    const next: Stored = { ...stored.value }
    change(next)
    changed.value = { ...changed.value, [key.value]: next }
    try {
      localStorage.setItem(key.value, JSON.stringify(next))
    } catch {
      // storage unavailable: the choice lasts until the page is closed
    }
  }

  const pinIds = computed<string[]>(
    () => stored.value.pins ?? DEFAULT_PINS.filter((d) => !d.permission || auth.can(d.permission, property.currentId)).map((d) => d.id),
  )
  const visible = (id: string): ActiveNav | undefined => nav.byId(id)
  const pinned = computed<ActiveNav[]>(() => pinIds.value.map(visible).filter((x): x is ActiveNav => !!x))
  // The pages opened last, without the page the person is on and without the pinned ones: those are in the menu already, and the list is for getting back to the others.
  const here = computed(() => (route ? activeNav(route.path, nav.sections.value)?.item.id : undefined))
  const recent = computed<ActiveNav[]>(() =>
    (stored.value.recent ?? [])
      .filter((id) => id !== here.value && !pinIds.value.includes(id))
      .map(visible)
      .filter((x): x is ActiveNav => !!x)
      .slice(0, RECENT_LIMIT),
  )
  const showRecent = computed(() => stored.value.showRecent ?? true)

  const isPinned = (id: string): boolean => pinIds.value.includes(id)
  function togglePin(id: string): void {
    update((s) => {
      const now = pinIds.value
      s.pins = now.includes(id) ? now.filter((x) => x !== id) : [...now, id]
    })
  }
  function setShowRecent(value: boolean): void {
    update((s) => {
      s.showRecent = value
    })
  }

  /** Puts the page the person is on first among the last opened (once, never twice, at most five). */
  function track(path: string | undefined = route?.path): void {
    if (path === undefined) return
    const hit = activeNav(path, nav.sections.value)
    if (!hit) return
    const id = hit.item.id
    if (stored.value.recent?.[0] === id) return
    update((s) => {
      s.recent = [id, ...(s.recent ?? []).filter((x) => x !== id)].slice(0, RECENT_LIMIT)
    })
  }

  return { pinned, recent, showRecent, isPinned, togglePin, setShowRecent, track }
}

/** For tests: forgets what was changed in memory (the storage is the test's to clear). */
export function resetNavPins(): void {
  changed.value = {}
}
