<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { PosFailedJob, PosPrinter, PosStation } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { debounce, usePosEvents } from '@/composables/usePosEvents'
import { t, te } from '@/i18n'
import { formatDateTime } from '@/utils/format'

/**
 * What the cashier needs to know about the paper (docs/architecture/34-pos.md 20): a chip that says whether the printers of this outlet are ready ("Printer offline" when the agent has been silent for
 * more than 90 seconds or the printer says it has no paper), and the jobs that failed for good with a Reprint that asks for a reason. The browser can always print the bill: that stays on the bill.
 */
const props = defineProps<{ propertyId: number; outletId: number; receiptPrinterId: number | null; orderMode: string; canReprint: boolean }>()
const emit = defineEmits<{ (e: 'reprinted'): void }>()

const printers = ref<PosPrinter[]>([])
const stations = ref<PosStation[]>([])
const failed = ref<PosFailedJob[]>([])

async function load(): Promise<void> {
  const path = { params: { path: { propertyId: props.propertyId } } }
  try {
    const [p, s] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/pos/printers', path),
      api.GET('/api/v1/properties/{propertyId}/pos/stations', path),
    ])
    printers.value = p.data?.data ?? []
    stations.value = s.data?.data ?? []
  } catch {
    printers.value = []
  }
  if (!props.canReprint) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/pos/outlets/{id}/failed-print-jobs', { params: { path: { propertyId: props.propertyId, id: props.outletId } } })
    failed.value = data?.data ?? []
  } catch {
    failed.value = []
  }
}
watch(() => [props.propertyId, props.outletId], () => void load(), { immediate: true })
const refresh = debounce(() => void load(), 300)
usePosEvents({ propertyId: () => props.propertyId, channels: () => ['printers'], onEvent: () => refresh(), onReload: () => void load() })
// the silence of an agent is a matter of time, not of an event: look again now and then
const timer = setInterval(() => void load(), 30_000)
onBeforeUnmount(() => clearInterval(timer))

// the printers this outlet depends on: its receipt printer and, when it sends tickets to print, the printers of the stations
const mine = computed<PosPrinter[]>(() => {
  const ids = new Set<number>()
  if (props.receiptPrinterId) ids.add(props.receiptPrinterId)
  if (props.orderMode === 'PRINT') for (const s of stations.value) if (s.printer_id && s.is_active) ids.add(s.printer_id)
  return printers.value.filter((p) => ids.has(p.id) && p.is_active)
})
// One chip for each printer that has a problem, with its own cause; one "ready" chip when every printer is fine.
const BAD = ['OFFLINE', 'PAPER_OUT', 'COVER_OPEN']
interface Chip { key: string; level: 'ok' | 'bad' | 'unknown'; label: string }
const chips = computed<Chip[]>(() => {
  if (mine.value.length === 0) return []
  const out: Chip[] = []
  for (const p of mine.value) {
    const st = p.state ?? 'NO_DEVICE'
    if (BAD.includes(st)) out.push({ key: `p${p.id}`, level: 'bad', label: t(`posPrintStatus.bad_${st}`, { name: p.name }) })
    else if (st !== 'ONLINE') out.push({ key: `p${p.id}`, level: 'unknown', label: t('posPrintStatus.unknown', { name: p.name }) })
  }
  return out.length > 0 ? out : [{ key: 'ok', level: 'ok', label: t('posPrintStatus.ok') }]
})
const anyBad = computed(() => chips.value.some((c) => c.level === 'bad'))

// ---- reprint
const target = ref<PosFailedJob | null>(null)
const reason = ref('')
const busy = ref(false)
const reprintError = ref<ApiError | null>(null)
function ask(j: PosFailedJob): void {
  target.value = j
  reason.value = ''
  reprintError.value = null
}
async function reprint(): Promise<void> {
  const j = target.value
  if (!j) return
  busy.value = true
  reprintError.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/pos/print-jobs/{id}/reprint', { params: { path: { propertyId: props.propertyId, id: j.id } }, body: { reason: reason.value.trim() } })
    target.value = null
    emit('reprinted')
    await load()
  } catch (e) {
    reprintError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
const what = (j: PosFailedJob): string => [t(`posPrintStatus.kind_${j.kind}`), j.bill_number].filter(Boolean).join(' · ')
const why = (code: string): string => (te(`posPrintQueue.error_${code}`) ? t(`posPrintQueue.error_${code}`) : code)
</script>

<template>
  <div>
  <div v-if="chips.length > 0 || failed.length > 0" class="flex flex-col gap-2" data-testid="print-status">
    <div class="flex flex-wrap gap-2">
      <div
        v-for="c in chips" :key="c.key" class="inline-flex w-fit items-center gap-2 rounded-full border px-3 py-1 text-sm"
        :class="c.level === 'bad' ? 'border-destructive bg-destructive/10 text-destructive' : c.level === 'ok' ? 'border-border bg-card' : 'border-border bg-muted text-muted-foreground'"
        :data-level="c.level" data-testid="printer-chip" role="status"
      >
        <span class="size-2 rounded-full" :class="c.level === 'bad' ? 'bg-destructive' : c.level === 'ok' ? 'bg-primary' : 'bg-muted-foreground'" aria-hidden="true" />
        {{ c.label }}
      </div>
    </div>
    <p v-if="anyBad" class="m-0 text-xs text-muted-foreground" data-testid="printer-chip-hint">{{ t('posPrintStatus.browserHint') }}</p>

    <section v-if="failed.length > 0" class="rounded-lg border border-destructive bg-destructive/5 p-3" data-testid="failed-jobs">
      <h3 class="m-0 text-sm font-semibold text-destructive">{{ t('posPrintStatus.failedTitle', { n: failed.length }) }}</h3>
      <ul class="m-0 mt-2 list-none divide-y divide-border p-0">
        <li v-for="j in failed" :key="j.id" class="flex flex-wrap items-center gap-2 py-2 text-sm" :data-testid="`failed-${j.id}`">
          <div class="min-w-0 flex-1">
            <strong class="block truncate">{{ what(j) }}</strong>
            <span class="text-xs text-muted-foreground">{{ j.printer_name }} · {{ why(j.error_code) }} · {{ formatDateTime(j.created_at) }}</span>
          </div>
          <Button variant="outline" size="sm" :data-testid="`reprint-${j.id}`" @click="ask(j)">{{ t('posPrintStatus.reprint') }}</Button>
        </li>
      </ul>
    </section>
  </div>

  <Dialog :open="target !== null" @update:open="(v) => { if (!v) target = null }">
    <DialogContent class="max-w-md p-5" data-testid="reprint-dialog" @interact-outside.prevent>
      <DialogTitle>{{ t('posPrintStatus.reprintTitle') }}</DialogTitle>
      <DialogDescription class="mb-3 mt-1">{{ t('posPrintStatus.reprintHint') }}</DialogDescription>
      <ErrorNotice v-if="reprintError" :error="reprintError" inline />
      <form class="flex flex-col gap-3" novalidate @submit.prevent="reprint">
        <FormField :label="t('posPrintStatus.reason')" :error="reprintError?.fieldMessage('reason')">
          <template #default="{ id }"><Input :id="id" v-model="reason" name="reason" maxlength="200" autocomplete="off" data-testid="reprint-reason" /></template>
        </FormField>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="target = null">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy || !reason.trim()" data-testid="reprint-submit">{{ t('posPrintStatus.reprint') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
  </div>
</template>
