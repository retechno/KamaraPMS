<script setup lang="ts">
import { ChevronLeft } from 'lucide-vue-next'
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { BlockType, Room, RoomBlock } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { i18n, t } from '@/i18n'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { blockColumns, windowDates } from '@/utils/blocks'
import { addDays, formatBusinessDate } from '@/utils/dates'

const auth = useAuthStore()
const property = usePropertyStore()

const rooms = ref<Room[]>([])
const blocks = ref<RoomBlock[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const saving = ref(false)
const days = ref(14)
const windowStart = ref('')
const cancelling = ref<RoomBlock | null>(null)
const cancelReason = ref('')

const canManage = computed(() => auth.can('room_block.manage', property.currentId))
const businessDate = computed(() => property.clock?.business_date ?? '')
const dates = computed(() => (windowStart.value ? windowDates(windowStart.value, days.value) : []))
const sortedRooms = computed(() =>
  rooms.value.filter((r) => r.is_active).sort((a, b) => a.room_number.localeCompare(b.room_number, undefined, { numeric: true })),
)
const roomNumber = (id: number) => rooms.value.find((r) => r.id === id)?.room_number ?? String(id)
// A calendar date has one weekday whatever the time zone: read it in UTC, in the language of the page.
const weekday = (d: string) => new Date(`${d}T00:00:00Z`).toLocaleDateString(i18n.global.locale.value, { weekday: 'short', timeZone: 'UTC' })
const isWeekend = (d: string) => [0, 6].includes(new Date(`${d}T00:00:00Z`).getUTCDay())

const blockColumnsDef = computed<Column<RoomBlock>[]>(() => [
  { key: 'room_id', label: t('roomBlocks.room') },
  { key: 'block_type', label: t('roomBlocks.type') },
  { key: 'start_date', label: t('roomBlocks.from'), format: 'date' as const },
  { key: 'end_date', label: t('roomBlocks.untilCol'), format: 'date' as const },
  { key: 'reason', label: t('roomBlocks.reason') },
  ...(canManage.value ? [{ key: 'actions', label: '', align: 'right' as const }] : []),
])

const blank = () => ({ room_id: 0, block_type: 'OOO' as BlockType, start_date: '', end_date: '', reason: '' })
const form = reactive(blank())
const fieldError = (field: string) => error.value?.fieldMessage(field)
const conflicts = computed(() => {
  const c = error.value?.context.conflicts
  return Array.isArray(c) ? (c as { type: string; id: number; reference?: string; from: string; to: string }[]) : []
})

// Blocks per room that are visible in the window, with their grid columns (one extra column holds the room label).
const rows = computed(() =>
  sortedRooms.value.map((room) => ({
    room,
    bars: blocks.value
      .filter((b) => b.room_id === room.id)
      .flatMap((b) => {
        const cols = windowStart.value ? blockColumns(b, windowStart.value, days.value) : null
        return cols ? [{ block: b, start: cols.start + 1, end: cols.end + 1 }] : []
      }),
  })),
)

async function load(): Promise<void> {
  const propertyId = property.currentId
  rooms.value = []
  blocks.value = []
  loaded.value = false
  if (propertyId === null) return
  if (!windowStart.value) windowStart.value = businessDate.value
  if (!windowStart.value) return // the business date is still loading; the watcher below retries
  try {
    const from = windowStart.value
    const to = addDays(from, days.value)
    const [r, b] = await Promise.all([
      fetchAll((cursor) =>
        api.GET('/api/v1/properties/{propertyId}/rooms', { params: { path: { propertyId }, query: { limit: 200, cursor } } }),
      ),
      fetchAll((cursor) =>
        api.GET('/api/v1/properties/{propertyId}/room-blocks', {
          params: { path: { propertyId }, query: { status: 'ACTIVE', from, to, limit: 200, cursor } },
        }),
      ),
    ])
    rooms.value = r
    blocks.value = b.sort((x, y) => x.start_date.localeCompare(y.start_date) || x.id - y.id)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

function shift(n: number): void {
  windowStart.value = addDays(windowStart.value, n)
  void load()
}

function backToToday(): void {
  windowStart.value = businessDate.value
  void load()
}

function prefill(roomId: number, date: string): void {
  if (!canManage.value) return
  Object.assign(form, blank(), { room_id: roomId, start_date: date, end_date: addDays(date, 1) })
  error.value = null
}

async function create(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null) return
  saving.value = true
  error.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/room-blocks', {
      params: { path: { propertyId } },
      body: { ...form, room_id: Number(form.room_id) },
    })
    Object.assign(form, blank())
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

async function cancel(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !cancelling.value) return
  saving.value = true
  error.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/room-blocks/{id}/cancel', {
      params: { path: { propertyId, id: cancelling.value.id } },
      body: { reason: cancelReason.value },
    })
    cancelling.value = null
    cancelReason.value = ''
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

watch(() => property.currentId, () => {
  windowStart.value = ''
  void load()
}, { immediate: true })
// The business date arrives after the property is selected.
watch(businessDate, (bd) => {
  if (bd && !windowStart.value) void load()
})
</script>

<template>
  <PageHeader :title="t('roomBlocks.title')">
    <template #actions>
      <Button variant="outline" size="sm" @click="shift(-7)"><ChevronLeft />{{ t('roomBlocks.week') }}</Button>
      <Button variant="outline" size="sm" @click="backToToday">{{ t('roomBlocks.businessDate') }}</Button>
      <Button variant="outline" size="sm" @click="shift(7)">{{ t('roomBlocks.week') }} &rarr;</Button>
      <NativeSelect v-model.number="days" class="w-28" :aria-label="t('roomBlocks.daysShown')" @change="load">
        <option :value="14">{{ t('roomBlocks.days', { n: 14 }) }}</option>
        <option :value="28">{{ t('roomBlocks.days', { n: 28 }) }}</option>
      </NativeSelect>
    </template>
  </PageHeader>

  <div v-if="error" class="alert" role="alert" data-testid="form-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <ul v-if="conflicts.length" class="m-0 mt-1.5 pl-5" data-testid="conflicts">
      <li v-for="c in conflicts" :key="`${c.type}-${c.id}`">
        {{ c.type === 'STAY' ? t('roomBlocks.stayConflict', { ref: c.reference ?? c.id }) : t('roomBlocks.lineConflict', { id: c.id }) }}: {{ t('roomBlocks.conflictRange', { from: $date(c.from), to: $date(c.to) }) }}
      </li>
    </ul>
  </div>
  <p v-if="property.currentId === null" class="muted">{{ t('roomBlocks.selectProperty') }}</p>

  <template v-else>
    <Card class="mb-4 overflow-x-auto">
      <EmptyState v-if="loaded && !sortedRooms.length" :title="t('roomBlocks.noRooms')" data-testid="empty" />
      <div v-else class="grid min-w-[640px] text-xs" :style="{ '--days': days }" role="grid" :aria-label="t('roomBlocks.calendar')">
        <div class="grid grid-cols-[4.5rem_repeat(var(--days),minmax(1.75rem,1fr))] auto-rows-[2.25rem] border-b border-border text-muted-foreground">
          <div class="col-start-1 row-start-1" />
          <div
            v-for="d in dates"
            :key="d"
            :class="cn('flex flex-col items-center justify-center leading-tight', isWeekend(d) && 'bg-muted/60', d === businessDate && 'bg-primary/10 text-primary')"
            role="columnheader"
          >
            <small class="text-[10px] uppercase">{{ weekday(d) }}</small>{{ d.slice(8) }}<small>{{ d.slice(5, 7) }}</small>
          </div>
        </div>
        <div
          v-for="row in rows"
          :key="row.room.id"
          class="grid grid-cols-[4.5rem_repeat(var(--days),minmax(1.75rem,1fr))] auto-rows-[1.625rem] border-b border-border"
          role="row"
          :data-testid="`cal-${row.room.room_number}`"
        >
          <div class="col-start-1 row-start-1 flex items-center pl-2 font-semibold" role="rowheader">{{ row.room.room_number }}</div>
          <button
            v-for="(d, i) in dates"
            :key="d"
            type="button"
            :class="cn('cursor-pointer rounded-none border-0 border-l border-border bg-transparent p-0 hover:bg-accent disabled:cursor-default disabled:hover:bg-transparent', isWeekend(d) && 'bg-muted/40', d === businessDate && 'bg-primary/10')"
            :style="{ gridColumn: i + 2, gridRow: 1 }"
            :disabled="!canManage"
            :aria-label="t('roomBlocks.blockRoomFrom', { room: row.room.room_number, date: d })"
            @click="prefill(row.room.id, d)"
          />
          <div
            v-for="bar in row.bars"
            :key="bar.block.id"
            :class="cn('pointer-events-none z-[1] mx-px my-[3px] flex items-center rounded-md border px-1.5 font-bold text-foreground', bar.block.block_type === 'OOO' ? 'border-destructive bg-destructive/30' : 'border-warning bg-warning/30')"
            :style="{ gridColumn: `${bar.start} / ${bar.end}`, gridRow: 1 }"
            :title="`${bar.block.block_type}: ${bar.block.reason} (${bar.block.start_date} to ${bar.block.end_date})`"
            data-testid="bar"
          >
            {{ bar.block.block_type }}
          </div>
        </div>
      </div>
    </Card>

    <Card v-if="canManage" class="mb-4">
      <form novalidate data-testid="block-form" @submit.prevent="create">
        <CardHeader><CardTitle>{{ t('roomBlocks.newBlock') }}</CardTitle></CardHeader>
        <CardContent>
          <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <FormField :label="t('roomBlocks.room')" :error="fieldError('room_id')">
              <template #default="{ id, invalid }">
                <NativeSelect :id="id" v-model="form.room_id" name="room_id" :aria-invalid="invalid">
                  <option :value="0" disabled>{{ t('roomBlocks.selectRoom') }}</option>
                  <option v-for="r in sortedRooms" :key="r.id" :value="r.id">{{ r.room_number }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('roomBlocks.type')" :hint="t('roomBlocks.unsellable')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model="form.block_type" name="block_type">
                  <option value="OOO">{{ t('roomBlocks.ooo') }}</option>
                  <option value="OOS">{{ t('roomBlocks.oos') }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('roomBlocks.from')" :error="fieldError('start_date')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.start_date" name="start_date" type="date" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('roomBlocks.until')" :error="fieldError('end_date')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.end_date" name="end_date" type="date" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField class="sm:col-span-2 lg:col-span-4" :label="t('roomBlocks.reason')" :error="fieldError('reason')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.reason" name="reason" maxlength="500" :aria-invalid="invalid" /></template>
            </FormField>
          </div>
          <div class="mt-4 flex justify-end"><Button type="submit" :disabled="saving">{{ t('roomBlocks.create') }}</Button></div>
        </CardContent>
      </form>
    </Card>

    <Card class="mb-4">
      <CardHeader><CardTitle>{{ t('roomBlocks.activeInView') }}</CardTitle></CardHeader>
      <CardContent>
        <p v-if="loaded && !blocks.length" class="m-0 text-sm text-muted-foreground">{{ t('roomBlocks.noBlocks') }}</p>
        <DataTable v-else-if="blocks.length" :columns="blockColumnsDef" :rows="blocks" row-key="id" :row-test-id="(b) => `block-${b.id}`" :caption="t('roomBlocks.activeInView')">
          <template #cell-room_id="{ row: b }"><b>{{ roomNumber(b.room_id) }}</b></template>
          <template #cell-block_type="{ row: b }"><Badge :variant="b.block_type === 'OOO' ? 'destructive' : 'warning'">{{ b.block_type }}</Badge></template>
          <template #cell-start_date="{ row: b }">{{ formatBusinessDate(b.start_date) }}</template>
          <template #cell-end_date="{ row: b }">{{ formatBusinessDate(b.end_date) }}</template>
          <template #cell-actions="{ row: b }"><Button variant="outline" size="sm" @click="cancelling = b; cancelReason = ''">{{ t('roomBlocks.release') }}</Button></template>
        </DataTable>
      </CardContent>
    </Card>

    <Card v-if="cancelling" class="mb-4 border-primary/50">
      <form novalidate data-testid="cancel-form" @submit.prevent="cancel">
        <CardHeader><CardTitle>{{ t('roomBlocks.releaseTitle', { room: roomNumber(cancelling.room_id) }) }}</CardTitle></CardHeader>
        <CardContent>
          <FormField :label="t('roomBlocks.reason')" :error="fieldError('reason')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="cancelReason" name="cancel_reason" maxlength="500" :aria-invalid="invalid" /></template>
          </FormField>
          <div class="mt-4 flex justify-end gap-2">
            <Button type="button" variant="outline" @click="cancelling = null">{{ t('roomBlocks.keepBlock') }}</Button>
            <Button type="submit" :disabled="saving">{{ t('roomBlocks.releaseBlock') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>
  </template>
</template>
