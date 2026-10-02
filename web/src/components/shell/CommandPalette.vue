<script setup lang="ts">
import { CornerDownLeft, FileText, Search, User } from 'lucide-vue-next'
import { computed, nextTick, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { visibleNavigation } from '@/navigation'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

/**
 * The quick finder (Ctrl+K): it jumps to a page, and, from two letters on, looks up guests and reservations with the
 * list endpoints those pages already use (only what the role may read). A failed lookup just shows nothing.
 */
const open = defineModel<boolean>('open', { required: true })

const auth = useAuthStore()
const property = usePropertyStore()
const router = useRouter()

interface Entry {
  key: string
  group: 'pages' | 'guests' | 'reservations'
  label: string
  hint?: string
  to: string
}

const query = ref('')
const remote = ref<Entry[]>([])
const cursor = ref(0)
const input = ref<HTMLInputElement | null>(null)
let seq = 0
let timer: ReturnType<typeof setTimeout> | undefined

const pages = computed<Entry[]>(() =>
  visibleNavigation(auth.isAdmin).flatMap((s) =>
    s.items.map((i) => ({
      key: `page-${i.id}`,
      group: 'pages' as const,
      label: t(`nav.items.${i.id}` as never),
      hint: t(`nav.sections.${s.id}` as never),
      to: i.to,
    })),
  ),
)

const matchedPages = computed(() => {
  const q = query.value.trim().toLowerCase()
  const all = pages.value
  return (q ? all.filter((p) => `${p.label} ${p.hint}`.toLowerCase().includes(q)) : all).slice(0, q ? 8 : 6)
})

const entries = computed(() => [...matchedPages.value, ...remote.value])
const groups = computed(() =>
  (['pages', 'guests', 'reservations'] as const)
    .map((g) => ({ id: g, items: entries.value.filter((e) => e.group === g) }))
    .filter((g) => g.items.length > 0),
)
const flat = computed(() => groups.value.flatMap((g) => g.items))

async function lookup(q: string): Promise<void> {
  const mine = ++seq
  const propertyId = property.currentId
  if (propertyId === null || q.length < 2) {
    remote.value = []
    return
  }
  const found: Entry[] = []
  const jobs: Promise<void>[] = []
  if (auth.can('guest.read', propertyId)) {
    jobs.push(
      api
        .GET('/api/v1/guests', { params: { query: { q, property_id: propertyId, limit: 5 } } })
        .then(({ data }) => {
          for (const g of data?.data ?? []) {
            found.push({ key: `guest-${g.id}`, group: 'guests', label: [g.first_name, g.last_name].filter(Boolean).join(' '), hint: g.code, to: `/guests/${g.id}` })
          }
        })
        .catch(() => undefined),
    )
  }
  if (auth.can('reservation.read', propertyId)) {
    jobs.push(
      api
        .GET('/api/v1/properties/{propertyId}/reservations', { params: { path: { propertyId }, query: { q, limit: 5 } } })
        .then(({ data }) => {
          for (const r of data?.data ?? []) {
            found.push({ key: `res-${r.id}`, group: 'reservations', label: r.confirmation_number, hint: r.guest_name, to: `/reservations/${r.id}` })
          }
        })
        .catch(() => undefined),
    )
  }
  await Promise.all(jobs)
  if (mine === seq) remote.value = found
}

watch(query, (q) => {
  cursor.value = 0
  clearTimeout(timer)
  const v = q.trim()
  if (v.length < 2) {
    seq++
    remote.value = []
    return
  }
  timer = setTimeout(() => void lookup(v), 200)
})

watch(open, async (isOpen) => {
  if (isOpen) {
    query.value = ''
    remote.value = []
    cursor.value = 0
    await nextTick()
    input.value?.focus()
  } else {
    clearTimeout(timer)
  }
})

function go(entry: Entry | undefined): void {
  if (!entry) return
  open.value = false
  void router.push(entry.to)
}

function onKey(event: KeyboardEvent): void {
  const n = flat.value.length
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    if (n) cursor.value = (cursor.value + 1) % n
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    if (n) cursor.value = (cursor.value - 1 + n) % n
  } else if (event.key === 'Enter') {
    event.preventDefault()
    go(flat.value[cursor.value])
  }
}

const groupTitle = (g: Entry['group']): string => t(g === 'pages' ? 'shell.searchPages' : g === 'guests' ? 'shell.searchGuests' : 'shell.searchReservations')
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="p-0" data-testid="command-palette">
      <DialogTitle class="sr-only">{{ t('shell.search') }}</DialogTitle>
      <DialogDescription class="sr-only">{{ t('shell.searchHint') }}</DialogDescription>
      <div class="flex items-center gap-2 border-b border-border px-4">
        <Search class="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
        <input
          ref="input"
          v-model="query"
          type="text"
          role="combobox"
          aria-expanded="true"
          aria-controls="palette-results"
          :placeholder="t('shell.searchPlaceholder')"
          class="h-12 w-full border-0 bg-transparent text-sm text-foreground outline-none"
          data-testid="palette-input"
          @keydown="onKey"
        />
      </div>
      <div id="palette-results" role="listbox" class="max-h-[50vh] overflow-y-auto p-2" data-testid="palette-results">
        <p v-if="!flat.length" class="px-3 py-6 text-center text-sm text-muted-foreground" data-testid="palette-empty">{{ t('shell.searchNothing') }}</p>
        <template v-for="g in groups" :key="g.id">
          <p class="px-3 pb-1 pt-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">{{ groupTitle(g.id) }}</p>
          <button
            v-for="e in g.items"
            :key="e.key"
            type="button"
            role="option"
            :aria-selected="flat[cursor]?.key === e.key"
            :data-testid="`palette-${e.key}`"
            :class="cn('flex w-full cursor-pointer items-center gap-3 rounded-md border-0 bg-transparent px-3 py-2 text-left text-sm text-foreground', flat[cursor]?.key === e.key && 'bg-accent')"
            @mousemove="cursor = flat.findIndex((x) => x.key === e.key)"
            @click="go(e)"
          >
            <User v-if="e.group === 'guests'" class="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            <FileText v-else class="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            <span class="flex-1 truncate">{{ e.label }}</span>
            <span v-if="e.hint" class="truncate text-xs text-muted-foreground">{{ e.hint }}</span>
            <CornerDownLeft v-if="flat[cursor]?.key === e.key" class="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
          </button>
        </template>
      </div>
    </DialogContent>
  </Dialog>
</template>
