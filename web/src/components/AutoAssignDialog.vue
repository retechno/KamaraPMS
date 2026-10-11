<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import type { FreeRoom } from '@/api/types'
import StatusBadge from '@/components/app/StatusBadge.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { fetchFreeRooms } from '@/composables/useFreeRooms'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { assignFailure, assignRoom } from '@/utils/assignRoom'
import { formatDate } from '@/utils/format'
import { hasRequestedBed, isRoomReady } from '@/utils/freeRooms'
import { type AssignLine, freeRoomsKey, proposeAssignments } from '@/utils/roomAssign'

/**
 * "Assign automatically": a room is proposed for each room of a reservation that has none (see `proposeAssignments`), shown here first, and saved one by one when the person confirms. By
 * default only rooms that are ready are proposed; "Include rooms that are not ready" adds the empty rooms that are dirty or being cleaned, after the ready ones (a room that a guest still occupies
 * is never offered: the server does not list it as free). The saving goes in order, one request after the other, because the reservations of a hotel change as they are saved: the version of a
 * reservation that has two rooms is the one the answer of its first assignment gave. A room that cannot be assigned (taken meanwhile, the reservation changed) is marked and does not stop the others.
 */
const props = defineProps<{ lines: AssignLine[] }>()
const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ changed: [assigned: number] }>()

const property = usePropertyStore()
const includeNotReady = ref(false)
const loading = ref(false)
// The lines as they were when the dialog was opened: what the parent reads again meanwhile (a room that was assigned leaves its list) does not take a row out of what the person is looking at.
const snapshot = ref<AssignLine[]>([])
const free = ref<Map<string, FreeRoom[]>>(new Map())
const unread = ref<Set<string>>(new Set()) // the questions that could not be read
const running = ref(false)
const finished = ref(false)
type Outcome = { state: 'ok' } | { state: 'failed'; message: string } | { state: 'running' }
const outcomes = reactive<Record<string, Outcome>>({})
let assignedCount = 0

const requiresInspection = computed(() => property.current?.require_room_inspection_for_checkin ?? false)
const proposals = computed(() => proposeAssignments(snapshot.value, free.value, { includeNotReady: includeNotReady.value, requiresInspection: requiresInspection.value }))
const toSave = computed(() => proposals.value.filter((p) => p.room))
const okCount = computed(() => Object.values(outcomes).filter((o) => o.state === 'ok').length)
const failedCount = computed(() => Object.values(outcomes).filter((o) => o.state === 'failed').length)

async function readRooms(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null) return
  loading.value = true
  const keys = new Map(snapshot.value.map((l) => [freeRoomsKey(l), l]))
  const next = new Map<string, FreeRoom[]>()
  const missed = new Set<string>()
  await Promise.all(
    [...keys].map(async ([key, l]) => {
      try {
        next.set(key, await fetchFreeRooms(propertyId, { typeId: l.typeId, arrival: l.arrival, departure: l.departure }, property.clock?.business_date ?? ''))
      } catch {
        missed.add(key)
      }
    }),
  )
  free.value = next
  unread.value = missed
  loading.value = false
}

// Every time the dialog is opened the rooms are read again, and what was done before is forgotten.
watch(open, (isOpen) => {
  if (!isOpen) return
  snapshot.value = [...props.lines]
  for (const k of Object.keys(outcomes)) delete outcomes[k]
  includeNotReady.value = false
  finished.value = false
  assignedCount = 0
  void readRooms()
}, { immediate: true })

async function save(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || running.value) return
  running.value = true
  const versions = new Map<number, number>() // the latest version of each reservation, from the answers
  for (const { line, room } of toSave.value) {
    if (!room) continue
    outcomes[line.key] = { state: 'running' }
    try {
      const res = await assignRoom(propertyId, { reservationId: line.reservationId, lineId: line.lineId, version: versions.get(line.reservationId) ?? line.version }, room.room_id)
      versions.set(line.reservationId, res.version)
      outcomes[line.key] = { state: 'ok' }
      assignedCount++
    } catch (e) {
      outcomes[line.key] = { state: 'failed', message: assignFailure(e).message }
    }
  }
  running.value = false
  finished.value = true
  emit('changed', assignedCount)
}

const stay = (l: AssignLine): string => `${formatDate(l.arrival)} → ${formatDate(l.departure)}`
const ready = (r: FreeRoom): boolean => isRoomReady(r.housekeeping_status, requiresInspection.value)
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="max-w-3xl p-5" data-testid="auto-assign">
      <DialogTitle class="text-lg">{{ t('autoAssign.title') }}</DialogTitle>
      <DialogDescription class="mt-1 text-sm text-muted-foreground">{{ t('autoAssign.intro') }}</DialogDescription>

      <label class="mt-3 flex items-start gap-2 text-sm">
        <input v-model="includeNotReady" type="checkbox" name="include_not_ready" class="mt-0.5 size-4 accent-primary" :disabled="running || finished" data-testid="include-not-ready" />
        <span>{{ t('autoAssign.includeNotReady') }}<small class="block text-xs text-muted-foreground">{{ t('autoAssign.includeHint') }}</small></span>
      </label>

      <p v-if="loading" class="m-0 mt-4 text-sm text-muted-foreground" data-testid="auto-loading">{{ t('autoAssign.loadingRooms') }}</p>
      <p v-else-if="!snapshot.length" class="m-0 mt-4 text-sm text-muted-foreground" data-testid="auto-nothing">{{ t('autoAssign.nothing') }}</p>
      <div v-else class="mt-3 max-h-[50vh] overflow-auto">
        <table class="w-full border-collapse text-sm" data-testid="proposals">
          <thead>
            <tr class="text-left text-xs uppercase tracking-wide text-muted-foreground">
              <th class="py-1.5 pr-2 font-semibold">{{ t('autoAssign.reservation') }}</th>
              <th class="py-1.5 pr-2 font-semibold">{{ t('autoAssign.type') }}</th>
              <th class="py-1.5 pr-2 font-semibold">{{ t('autoAssign.stay') }}</th>
              <th class="py-1.5 font-semibold">{{ t('autoAssign.proposed') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="p in proposals" :key="p.line.key" class="border-t border-border align-top" :data-testid="`proposal-${p.line.key}`">
              <td class="py-2 pr-2"><span class="font-medium">{{ p.line.confirmation }}</span><span class="block text-xs text-muted-foreground">{{ p.line.guest || '—' }}</span></td>
              <td class="py-2 pr-2">{{ p.line.typeCode }}</td>
              <td class="py-2 pr-2 whitespace-nowrap text-xs">{{ stay(p.line) }}</td>
              <td class="py-2">
                <template v-if="outcomes[p.line.key]?.state === 'ok'">
                  <b data-testid="proposed-room">{{ p.room?.room_number }}</b> <span class="text-success-text" data-testid="outcome-ok">✓ {{ t('autoAssign.done') }}</span>
                </template>
                <template v-else-if="outcomes[p.line.key]?.state === 'failed'">
                  <b>{{ p.room?.room_number }}</b>
                  <span class="block text-xs text-destructive" role="alert" data-testid="outcome-failed">{{ t('autoAssign.failed', { reason: (outcomes[p.line.key] as { message: string }).message }) }}</span>
                </template>
                <template v-else-if="outcomes[p.line.key]?.state === 'running'"><b>{{ p.room?.room_number }}</b> <span class="text-muted-foreground" data-testid="outcome-running">{{ t('autoAssign.running') }}</span></template>
                <template v-else-if="p.room">
                  <span class="inline-flex flex-wrap items-center gap-1.5">
                    <b data-testid="proposed-room">{{ p.room.room_number }}</b>
                    <StatusBadge domain="housekeeping" :status="p.room.housekeeping_status" :data-testid="`proposed-status-${p.line.key}`" :data-ready="ready(p.room)" />
                    <span v-if="hasRequestedBed(p.room, p.line.requestedBedTypeId)" class="text-xs text-success-text" :title="t('bedTypes.matches')" data-testid="proposed-bed">✓ {{ p.room.bed_type_name }}</span>
                  </span>
                </template>
                <span v-else-if="unread.has(freeRoomsKey(p.line))" class="text-xs text-destructive" data-testid="proposal-unread">{{ t('autoAssign.loadFailed', { type: p.line.typeCode }) }}</span>
                <span v-else class="text-muted-foreground" data-testid="proposal-none">{{ t('autoAssign.noMatch') }}</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <p v-if="finished" class="m-0 mt-3 text-sm font-medium" role="status" data-testid="auto-summary">{{ t('autoAssign.summary', { ok: okCount, failed: failedCount }) }}</p>

      <div class="mt-4 flex flex-wrap justify-end gap-2">
        <Button type="button" variant="outline" :disabled="running" data-testid="auto-close" @click="open = false">{{ t('autoAssign.close') }}</Button>
        <Button v-if="!finished" type="button" :disabled="running || loading || !toSave.length" data-testid="auto-confirm" @click="save">
          {{ running ? t('autoAssign.running') : t('autoAssign.confirm', { n: toSave.length }, toSave.length) }}
        </Button>
      </div>
    </DialogContent>
  </Dialog>
</template>
