<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { PosArea, PosMark, PosOutlet, PosTable } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'

/**
 * The editor of the floor plan of an outlet (docs/architecture/34-pos.md 3.2): areas, tables with a position, a shape and a capacity, and marks that only decorate (a window, a door, the
 * cashier). The plan is saved whole with the version that was loaded: if someone saved in between the editor says so and reloads. A table that is taken off the plan is switched off, not deleted.
 */
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const CELL = 20 // pixels of a grid unit

const outlets = ref<PosOutlet[]>([])
const outletId = ref(0)
const areas = ref<PosArea[]>([])
const areaId = ref(0)
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const notice = ref('')
const saving = ref(false)
const conflict = ref(false)
const dirty = ref(false)

interface Draft { name: string; sort_order: number; grid_w: number; grid_h: number; layout_version: number; tables: PosTable[]; marks: PosMark[] }
const draft = reactive<Draft>({ name: '', sort_order: 0, grid_w: 40, grid_h: 24, layout_version: 1, tables: [], marks: [] })
type Sel = { kind: 'table' | 'mark'; index: number } | null
const sel = ref<Sel>(null)

const area = computed(() => areas.value.find((a) => a.id === areaId.value) ?? null)
const selTable = computed(() => (sel.value?.kind === 'table' ? draft.tables[sel.value.index] : undefined))
const selMark = computed(() => (sel.value?.kind === 'mark' ? draft.marks[sel.value.index] : undefined))

function adopt(a: PosArea | null): void {
  sel.value = null
  dirty.value = false
  if (!a) {
    Object.assign(draft, { name: '', sort_order: 0, grid_w: 40, grid_h: 24, layout_version: 1, tables: [], marks: [] })
    return
  }
  Object.assign(draft, {
    name: a.name, sort_order: a.sort_order, grid_w: a.grid_w, grid_h: a.grid_h, layout_version: a.layout_version,
    tables: a.tables.filter((x) => x.is_active).map((x) => ({ ...x })), marks: a.marks.map((x) => ({ ...x })),
  })
}

async function loadOutlets(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  if (propertyId === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/pos/outlets', { params: { path: { propertyId }, query: { active: true } } })
    outlets.value = data?.data ?? []
    if (!outlets.value.some((o) => o.id === outletId.value)) outletId.value = outlets.value[0]?.id ?? 0
    await loadPlan()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

async function loadPlan(keepArea?: number): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !outletId.value) {
    areas.value = []
    adopt(null)
    return
  }
  const { data } = await api.GET('/api/v1/properties/{propertyId}/pos/outlets/{id}/floor-plan', { params: { path: { propertyId, id: outletId.value } } })
  areas.value = data?.areas ?? []
  areaId.value = areas.value.find((a) => a.id === keepArea)?.id ?? areas.value[0]?.id ?? 0
  adopt(area.value)
}

function chooseArea(id: number): void {
  if (dirty.value && !window.confirm(t('pos.floor.discard'))) return
  areaId.value = id
  adopt(area.value)
}

// ---- new area
const aDialog = ref(false)
const aName = ref('')
const aError = ref<ApiError | null>(null)
async function createArea(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !outletId.value) return
  aError.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/pos/outlets/{id}/areas', { params: { path: { propertyId, id: outletId.value } }, body: { name: aName.value.trim(), sort_order: areas.value.length + 1 } })
    aDialog.value = false
    aName.value = ''
    await loadPlan(data?.id)
  } catch (e) {
    aError.value = e instanceof ApiError ? e : null
  }
}

// ---- editing
function freeSpot(w: number, h: number): { x: number; y: number } {
  for (let y = 0; y + h <= draft.grid_h; y++) {
    for (let x = 0; x + w <= draft.grid_w; x++) {
      const clash = [...draft.tables, ...draft.marks].some((o) => x < o.x + o.w && o.x < x + w && y < o.y + o.h && o.y < y + h)
      if (!clash) return { x, y }
    }
  }
  return { x: 0, y: 0 }
}
function nextCode(): string {
  const used = new Set(draft.tables.map((x) => x.code))
  for (let n = 1; n < 1000; n++) if (!used.has(String(n))) return String(n)
  return ''
}
function addTable(shape: 'SQUARE' | 'RECT' | 'ROUND' = 'SQUARE'): void {
  const w = shape === 'RECT' ? 6 : 4
  const h = 4
  const { x, y } = freeSpot(w, h)
  draft.tables.push({ code: nextCode(), capacity: 2, shape, x, y, w, h, is_active: true })
  sel.value = { kind: 'table', index: draft.tables.length - 1 }
  dirty.value = true
}
const markKind = ref<'WINDOW' | 'DOOR' | 'CASHIER' | 'OTHER'>('DOOR')
function addMark(): void {
  const w = 6
  const h = 2
  const { x, y } = freeSpot(w, h)
  draft.marks.push({ kind: markKind.value, label: t(`pos.floor.mark_${markKind.value}`), x, y, w, h })
  sel.value = { kind: 'mark', index: draft.marks.length - 1 }
  dirty.value = true
}
function removeSelected(): void {
  if (!sel.value) return
  if (sel.value.kind === 'table') draft.tables.splice(sel.value.index, 1)
  else draft.marks.splice(sel.value.index, 1)
  sel.value = null
  dirty.value = true
}

// dragging by pointer, in grid units
let drag: { kind: 'table' | 'mark'; index: number; sx: number; sy: number; ox: number; oy: number } | null = null
function down(e: PointerEvent, kind: 'table' | 'mark', index: number): void {
  const item = kind === 'table' ? draft.tables[index]! : draft.marks[index]!
  sel.value = { kind, index }
  drag = { kind, index, sx: e.clientX, sy: e.clientY, ox: item.x, oy: item.y }
  ;(e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId)
}
function move(e: PointerEvent): void {
  if (!drag) return
  const item = drag.kind === 'table' ? draft.tables[drag.index]! : draft.marks[drag.index]!
  const x = Math.round(drag.ox + (e.clientX - drag.sx) / CELL)
  const y = Math.round(drag.oy + (e.clientY - drag.sy) / CELL)
  const nx = Math.max(0, Math.min(draft.grid_w - item.w, x))
  const ny = Math.max(0, Math.min(draft.grid_h - item.h, y))
  if (nx !== item.x || ny !== item.y) {
    item.x = nx
    item.y = ny
    dirty.value = true
  }
}
function up(): void {
  drag = null
}
const touch = (): void => { dirty.value = true }

async function save(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !areaId.value) return
  saving.value = true
  error.value = null
  conflict.value = false
  try {
    const { data } = await api.PUT('/api/v1/properties/{propertyId}/pos/areas/{id}/layout', {
      params: { path: { propertyId, id: areaId.value } },
      body: {
        name: draft.name.trim(), sort_order: draft.sort_order, grid_w: draft.grid_w, grid_h: draft.grid_h, layout_version: draft.layout_version,
        tables: draft.tables.map((x) => ({ id: x.id, code: x.code.trim(), capacity: x.capacity, shape: x.shape, x: x.x, y: x.y, w: x.w, h: x.h, is_active: true })),
        marks: draft.marks.map((m) => ({ kind: m.kind, label: m.label.trim(), x: m.x, y: m.y, w: m.w, h: m.h })),
      },
    })
    notice.value = t('pos.floor.saved')
    await loadPlan(data?.id ?? areaId.value)
  } catch (e) {
    if (e instanceof ApiError && e.code === 'VERSION_CONFLICT') conflict.value = true
    else error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}
async function reload(): Promise<void> {
  conflict.value = false
  await loadPlan(areaId.value)
}

const tableLabel = (x: PosTable): string => x.code
const shapeClass = (x: PosTable): string => (x.shape === 'ROUND' ? 'rounded-full' : 'rounded-lg')
watch(pid, loadOutlets, { immediate: true })
watch(outletId, () => { if (loaded.value) void loadPlan() })
</script>

<template>
  <PageHeader :title="t('pos.floor.title')">
    <template #actions>
      <NativeSelect v-if="outlets.length" v-model.number="outletId" name="outlet" class="w-auto" :aria-label="t('pos.floor.outlet')" data-testid="floor-outlet">
        <option v-for="o in outlets" :key="o.id" :value="o.id">{{ o.name }}</option>
      </NativeSelect>
    </template>
  </PageHeader>
  <p class="muted">{{ t('pos.floor.intro') }}</p>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="conflict" class="alert warning" role="alert" data-testid="floor-conflict">
    {{ t('pos.floor.conflict') }} <Button variant="outline" size="sm" class="ml-2" data-testid="floor-reload" @click="reload">{{ t('pos.floor.reload') }}</Button>
  </p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="loaded && outlets.length === 0" class="muted" data-testid="floor-no-outlets">{{ t('pos.floor.noOutlets') }}</p>

  <template v-else-if="loaded">
    <div class="mb-3 flex flex-wrap items-center gap-2" role="tablist" :aria-label="t('pos.floor.areas')" data-testid="floor-areas">
      <Button
        v-for="a in areas" :key="a.id" role="tab" size="sm" :variant="a.id === areaId ? 'default' : 'outline'" :aria-selected="a.id === areaId" :data-testid="`area-${a.id}`"
        @click="chooseArea(a.id)"
      >{{ a.name }}</Button>
      <Button size="sm" variant="outline" data-testid="area-new" @click="aDialog = true">{{ t('pos.floor.newArea') }}</Button>
    </div>
    <p v-if="areas.length === 0" class="muted" data-testid="floor-empty">{{ t('pos.floor.noAreas') }}</p>

    <div v-else class="grid items-start gap-4 xl:grid-cols-[minmax(0,1fr)_300px]">
      <div class="overflow-auto rounded-xl border border-border bg-card p-3" data-testid="floor-canvas-wrap">
        <div
          class="relative touch-none select-none rounded-lg border border-dashed border-border bg-[length:20px_20px]"
          :style="{ width: `${draft.grid_w * CELL}px`, height: `${draft.grid_h * CELL}px`, backgroundImage: 'linear-gradient(to right, var(--border, #e3e6ea) 1px, transparent 1px), linear-gradient(to bottom, var(--border, #e3e6ea) 1px, transparent 1px)' }"
          data-testid="floor-canvas" @pointermove="move" @pointerup="up" @pointercancel="up" @click.self="sel = null"
        >
          <div
            v-for="(m, i) in draft.marks" :key="`m${i}`" role="button" tabindex="0" :aria-label="m.label"
            class="absolute flex cursor-move items-center justify-center rounded border border-border bg-muted px-1 text-center text-xs text-muted-foreground"
            :class="sel?.kind === 'mark' && sel.index === i && 'ring-2 ring-primary'"
            :style="{ left: `${m.x * CELL}px`, top: `${m.y * CELL}px`, width: `${m.w * CELL}px`, height: `${m.h * CELL}px` }" :data-testid="`mark-${i}`"
            @pointerdown.stop="down($event, 'mark', i)" @keydown.enter="sel = { kind: 'mark', index: i }"
          >{{ m.label }}</div>
          <div
            v-for="(x, i) in draft.tables" :key="`t${i}`" role="button" tabindex="0" :aria-label="`${t('pos.floor.table')} ${x.code}`"
            class="absolute flex cursor-move flex-col items-center justify-center border-2 bg-background text-xs font-semibold" :class="[shapeClass(x), sel?.kind === 'table' && sel.index === i ? 'border-primary ring-2 ring-primary/40' : 'border-border']"
            :style="{ left: `${x.x * CELL}px`, top: `${x.y * CELL}px`, width: `${x.w * CELL}px`, height: `${x.h * CELL}px` }" :data-testid="`table-${i}`"
            @pointerdown.stop="down($event, 'table', i)" @keydown.enter="sel = { kind: 'table', index: i }"
          >
            <span class="text-sm">{{ tableLabel(x) }}</span>
            <span class="font-normal text-muted-foreground">{{ x.capacity }}</span>
          </div>
        </div>
      </div>

      <aside class="flex flex-col gap-3 rounded-xl border border-border bg-card p-4" data-testid="floor-side">
        <div class="flex flex-wrap gap-2">
          <Button size="sm" data-testid="table-add" @click="addTable('SQUARE')">{{ t('pos.floor.addTable') }}</Button>
          <Button size="sm" variant="outline" data-testid="table-add-round" @click="addTable('ROUND')">{{ t('pos.floor.addRound') }}</Button>
          <Button size="sm" variant="outline" data-testid="table-add-rect" @click="addTable('RECT')">{{ t('pos.floor.addRect') }}</Button>
        </div>
        <div class="flex gap-2">
          <NativeSelect v-model="markKind" name="mark_kind" class="flex-1" :aria-label="t('pos.floor.markKind')">
            <option v-for="k in ['WINDOW', 'DOOR', 'CASHIER', 'OTHER']" :key="k" :value="k">{{ t(`pos.floor.mark_${k}`) }}</option>
          </NativeSelect>
          <Button size="sm" variant="outline" data-testid="mark-add" @click="addMark">{{ t('pos.floor.addMark') }}</Button>
        </div>

        <div v-if="selTable" class="flex flex-col gap-2" data-testid="table-form">
          <h3 class="m-0 text-sm font-semibold">{{ t('pos.floor.table') }} {{ selTable.code }}</h3>
          <label class="text-xs text-muted-foreground">{{ t('pos.floor.code') }}<Input v-model="selTable.code" name="table_code" maxlength="10" class="mt-1 h-10" @input="touch" /></label>
          <label class="text-xs text-muted-foreground">{{ t('pos.floor.capacity') }}<Input v-model.number="selTable.capacity" type="number" min="1" max="99" name="table_capacity" class="mt-1 h-10" @input="touch" /></label>
          <label class="text-xs text-muted-foreground">{{ t('pos.floor.shape') }}
            <NativeSelect v-model="selTable.shape" name="table_shape" class="mt-1" @update:model-value="touch">
              <option value="SQUARE">{{ t('pos.floor.shape_SQUARE') }}</option><option value="RECT">{{ t('pos.floor.shape_RECT') }}</option><option value="ROUND">{{ t('pos.floor.shape_ROUND') }}</option>
            </NativeSelect>
          </label>
          <div class="grid grid-cols-2 gap-2">
            <label class="text-xs text-muted-foreground">{{ t('pos.floor.width') }}<Input v-model.number="selTable.w" type="number" min="1" max="60" name="table_w" class="mt-1 h-10" @input="touch" /></label>
            <label class="text-xs text-muted-foreground">{{ t('pos.floor.height') }}<Input v-model.number="selTable.h" type="number" min="1" max="60" name="table_h" class="mt-1 h-10" @input="touch" /></label>
            <label class="text-xs text-muted-foreground">X<Input v-model.number="selTable.x" type="number" min="0" name="table_x" class="mt-1 h-10" @input="touch" /></label>
            <label class="text-xs text-muted-foreground">Y<Input v-model.number="selTable.y" type="number" min="0" name="table_y" class="mt-1 h-10" @input="touch" /></label>
          </div>
        </div>
        <div v-else-if="selMark" class="flex flex-col gap-2" data-testid="mark-form">
          <h3 class="m-0 text-sm font-semibold">{{ t('pos.floor.mark') }}</h3>
          <label class="text-xs text-muted-foreground">{{ t('pos.floor.label') }}<Input v-model="selMark.label" name="mark_label" maxlength="40" class="mt-1 h-10" @input="touch" /></label>
          <div class="grid grid-cols-2 gap-2">
            <label class="text-xs text-muted-foreground">{{ t('pos.floor.width') }}<Input v-model.number="selMark.w" type="number" min="1" max="60" name="mark_w" class="mt-1 h-10" @input="touch" /></label>
            <label class="text-xs text-muted-foreground">{{ t('pos.floor.height') }}<Input v-model.number="selMark.h" type="number" min="1" max="60" name="mark_h" class="mt-1 h-10" @input="touch" /></label>
          </div>
        </div>
        <p v-else class="m-0 text-sm text-muted-foreground">{{ t('pos.floor.selectHint') }}</p>
        <Button v-if="sel" variant="outline" size="sm" data-testid="floor-remove" @click="removeSelected">{{ t('pos.floor.remove') }}</Button>

        <hr class="m-0 border-border" />
        <label class="text-xs text-muted-foreground">{{ t('pos.floor.areaName') }}<Input v-model="draft.name" name="area_name" maxlength="100" class="mt-1 h-10" @input="touch" /></label>
        <div class="grid grid-cols-2 gap-2">
          <label class="text-xs text-muted-foreground">{{ t('pos.floor.gridW') }}<Input v-model.number="draft.grid_w" type="number" min="10" max="200" name="grid_w" class="mt-1 h-10" @input="touch" /></label>
          <label class="text-xs text-muted-foreground">{{ t('pos.floor.gridH') }}<Input v-model.number="draft.grid_h" type="number" min="10" max="200" name="grid_h" class="mt-1 h-10" @input="touch" /></label>
        </div>
        <p class="m-0 text-xs text-muted-foreground">{{ t('pos.floor.counts', { tables: draft.tables.length, seats: draft.tables.reduce((n, x) => n + x.capacity, 0) }) }}</p>
        <Button :disabled="saving || !draft.name.trim()" data-testid="floor-save" @click="save">{{ t('common.save') }}</Button>
      </aside>
    </div>
  </template>

  <Dialog :open="aDialog" @update:open="(v) => (aDialog = v)">
    <DialogContent class="max-w-md p-5" data-testid="area-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="createArea">
        <DialogTitle>{{ t('pos.floor.newArea') }}</DialogTitle>
        <DialogDescription>{{ t('pos.floor.newAreaHint') }}</DialogDescription>
        <ErrorNotice v-if="aError" :error="aError" inline />
        <Input v-model="aName" name="area_name_new" maxlength="100" class="h-11" :placeholder="t('pos.floor.areaName')" :aria-label="t('pos.floor.areaName')" data-testid="area-name" />
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="aDialog = false">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="!aName.trim()" data-testid="area-create">{{ t('common.save') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
