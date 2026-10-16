<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { PosDevice, PosPrinter, PosPrintQueue, PosSetupIssue, PosStation } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import PosDevicesPanel from '@/components/pos/PosDevicesPanel.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { debounce, usePosEvents } from '@/composables/usePosEvents'
import { t, te } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { formatDateTime } from '@/utils/format'

/**
 * The places of production (kitchen, bar, pastry) and the printers (docs/architecture/34-pos.md 3.2). A station has a printer, a screen of the kitchen display, or both; a printer is a record the
 * print agent of the hotel will find (stage 3): nothing prints from here yet. The configuration check names what an outlet still lacks.
 */
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const stations = ref<PosStation[]>([])
const printers = ref<PosPrinter[]>([])
const devices = ref<PosDevice[]>([])
const issues = ref<PosSetupIssue[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const notice = ref('')

const queue = ref<PosPrintQueue | null>(null)
async function loadQueue(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/pos/print-jobs', { params: { path: { propertyId }, query: { limit: 30 } } })
    queue.value = data && Array.isArray(data.jobs) ? data : null
  } catch {
    queue.value = null // the queue is for who prints; the setup works without it
  }
}
const jobError = (code: string): string => (te(`posPrintQueue.error_${code}`) ? t(`posPrintQueue.error_${code}`) : code)
const refreshQueue = debounce(() => void loadQueue(), 200)
usePosEvents({
  propertyId: () => pid.value,
  channels: () => (pid.value === null ? [] : ['printers']),
  onEvent: () => refreshQueue(),
  onReload: () => void loadQueue(),
})

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  if (propertyId === null) return
  try {
    const path = { params: { path: { propertyId } } }
    const [s, p, c, d] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/pos/stations', path),
      api.GET('/api/v1/properties/{propertyId}/pos/printers', path),
      api.GET('/api/v1/properties/{propertyId}/pos/setup-check', path),
      api.GET('/api/v1/properties/{propertyId}/pos/devices', path),
    ])
    stations.value = s.data?.data ?? []
    printers.value = p.data?.data ?? []
    issues.value = c.data?.data ?? []
    devices.value = d.data?.data ?? []
    void loadQueue()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

// ---- stations
const sForm = reactive({ id: null as number | null, code: '', name: '', printer_id: 0, kds_enabled: false, late_after_minutes: 15, is_active: true, ticket_copies: 1 })
const sDialog = ref(false)
const sError = ref<ApiError | null>(null)
const busy = ref(false)
function editStation(s?: PosStation): void {
  Object.assign(sForm, s
    ? { id: s.id, code: s.code, name: s.name, printer_id: s.printer_id ?? 0, kds_enabled: s.kds_enabled, late_after_minutes: s.late_after_minutes, is_active: s.is_active, ticket_copies: s.ticket_copies ?? 1 }
    : { id: null, code: '', name: '', printer_id: 0, kds_enabled: false, late_after_minutes: 15, is_active: true, ticket_copies: 1 })
  sError.value = null
  sDialog.value = true
}
async function saveStation(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  sError.value = null
  const body = { code: sForm.code.trim().toUpperCase(), name: sForm.name.trim(), printer_id: sForm.printer_id || null, kds_enabled: sForm.kds_enabled, late_after_minutes: sForm.late_after_minutes, is_active: sForm.is_active, ticket_copies: sForm.ticket_copies }
  try {
    if (sForm.id === null) await api.POST('/api/v1/properties/{propertyId}/pos/stations', { params: { path: { propertyId } }, body })
    else await api.PUT('/api/v1/properties/{propertyId}/pos/stations/{id}', { params: { path: { propertyId, id: sForm.id } }, body })
    sDialog.value = false
    notice.value = t('pos.stations.saved')
    await load()
  } catch (e) {
    sError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

// ---- printers
const pForm = reactive({ id: null as number | null, name: '', kind: 'RECEIPT', connection_info: '', paper_width_mm: 80, codepage: 'CP437', opens_drawer: false, is_active: true, device_id: 0 })
const pDialog = ref(false)
const pError = ref<ApiError | null>(null)
function editPrinter(p?: PosPrinter): void {
  Object.assign(pForm, p
    ? { id: p.id, name: p.name, kind: p.kind, connection_info: p.connection_info, paper_width_mm: p.paper_width_mm, codepage: p.codepage, opens_drawer: p.opens_drawer, is_active: p.is_active, device_id: p.device_id ?? 0 }
    : { id: null, name: '', kind: 'RECEIPT', connection_info: '', paper_width_mm: 80, codepage: 'CP437', opens_drawer: false, is_active: true, device_id: 0 })
  pError.value = null
  pDialog.value = true
}
async function savePrinter(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  pError.value = null
  const body = {
    name: pForm.name.trim(), kind: pForm.kind as 'RECEIPT' | 'KITCHEN' | 'LABEL', connection_info: pForm.connection_info.trim(), paper_width_mm: pForm.paper_width_mm as 58 | 80,
    codepage: pForm.codepage.trim(), opens_drawer: pForm.opens_drawer, is_active: pForm.is_active, device_id: pForm.device_id || null,
  }
  try {
    if (pForm.id === null) await api.POST('/api/v1/properties/{propertyId}/pos/printers', { params: { path: { propertyId } }, body })
    else await api.PUT('/api/v1/properties/{propertyId}/pos/printers/{id}', { params: { path: { propertyId, id: pForm.id } }, body })
    pDialog.value = false
    notice.value = t('pos.printers.saved')
    await load()
  } catch (e) {
    pError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

// What the check found, one line per outlet + station + reason (not one per product): up to 5 products are named, then "+N more". A problem that stops an order reaching the kitchen is
// blocking (red, "Blocking"); the rest is advice (grey, "Advice"): the label says it, the colour is only a hint.
interface IssueLine { key: string; blocking: boolean; text: string; code: string }
const MAX_NAMED = 5
const issueLines = computed<IssueLine[]>(() => {
  const lines: IssueLine[] = []
  const groups = new Map<string, { head: PosSetupIssue; products: string[] }>()
  for (const i of issues.value) {
    if (!i.code.startsWith('PRODUCT_')) {
      lines.push({ key: `${i.code}|${i.outlet ?? ''}`, blocking: i.severity === 'ERROR', code: i.code, text: t(`pos.check.${i.code}`, { outlet: i.outlet ?? '' }) })
      continue
    }
    const key = `${i.outlet ?? ''}|${i.station ?? ''}|${i.reason ?? i.code}`
    const g = groups.get(key) ?? { head: i, products: [] }
    if (i.product) g.products.push(i.product)
    groups.set(key, g)
  }
  for (const [key, g] of groups) {
    const names = g.products.slice(0, MAX_NAMED).join(', ')
    const more = g.products.length > MAX_NAMED ? t('pos.check.more', { n: g.products.length - MAX_NAMED }) : ''
    const reason = g.head.reason ?? 'NO_STATION'
    const other = g.head.other_output ? `_${g.head.other_output}` : ''
    lines.push({
      key, blocking: g.head.severity === 'ERROR', code: g.head.code,
      text: t(`pos.check.group_${reason}${other}`, { outlet: g.head.outlet ?? '', station: g.head.station ?? '', n: g.products.length, products: names + more }),
    })
  }
  return lines.sort((a, b) => Number(b.blocking) - Number(a.blocking))
})
watch(pid, load, { immediate: true })
</script>

<template>
  <PageHeader :title="t('pos.stations.title')" />
  <p class="muted">{{ t('pos.stations.intro') }}</p>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>

  <template v-else-if="loaded">
    <section v-if="issues.length" class="mb-4 rounded-xl border border-border bg-card p-4" data-testid="setup-check">
      <h2 class="m-0 mb-2 text-sm font-semibold">{{ t('pos.check.title') }}</h2>
      <ul class="m-0 list-none space-y-1 p-0 text-sm">
        <li v-for="l in issueLines" :key="l.key" class="flex items-start gap-2" :class="l.blocking ? 'text-destructive' : 'text-muted-foreground'" :data-code="l.code" :data-level="l.blocking ? 'blocking' : 'advice'">
          <span class="mt-0.5 shrink-0 rounded border border-current px-1.5 text-[11px] font-semibold uppercase leading-5" data-testid="issue-level">{{ t(l.blocking ? 'pos.check.levelBlocking' : 'pos.check.levelAdvice') }}</span>
          <span class="min-w-0">{{ l.text }}</span>
        </li>
      </ul>
    </section>

    <PosDevicesPanel v-if="pid !== null" class="mb-4" :property-id="pid" @changed="load" />

    <div class="grid gap-4 lg:grid-cols-2">
      <section class="rounded-xl border border-border bg-card p-4" data-testid="stations">
        <header class="mb-3 flex items-center justify-between gap-2">
          <h2 class="m-0 text-base font-semibold">{{ t('pos.stations.stations') }}</h2>
          <Button size="sm" data-testid="station-new" @click="editStation()">{{ t('pos.stations.newStation') }}</Button>
        </header>
        <p v-if="stations.length === 0" class="muted" data-testid="stations-empty">{{ t('pos.stations.noStations') }}</p>
        <ul class="m-0 list-none divide-y divide-border p-0">
          <li v-for="s in stations" :key="s.id" class="flex min-h-11 flex-wrap items-center gap-2 py-2" :data-testid="`station-${s.code}`">
            <div class="min-w-0 flex-1">
              <strong class="block truncate text-sm">{{ s.name }}</strong>
              <span class="text-xs text-muted-foreground">{{ s.code }} · {{ s.printer_name ?? t('pos.stations.noPrinter') }}<template v-if="s.kds_enabled"> · {{ t('pos.stations.kds') }}</template> · {{ t('pos.stations.lateAfter', { n: s.late_after_minutes }) }}<template v-if="(s.ticket_copies ?? 1) > 1"> · {{ t('pos.stations.copiesN', { n: s.ticket_copies }) }}</template></span>
            </div>
            <Badge v-if="!s.is_active" variant="outline">{{ t('common.inactive') }}</Badge>
            <Button variant="outline" size="sm" :data-testid="`station-edit-${s.code}`" @click="editStation(s)">{{ t('common.edit') }}</Button>
          </li>
        </ul>
      </section>

      <section class="rounded-xl border border-border bg-card p-4" data-testid="printers">
        <header class="mb-3 flex items-center justify-between gap-2">
          <h2 class="m-0 text-base font-semibold">{{ t('pos.printers.title') }}</h2>
          <Button size="sm" data-testid="printer-new" @click="editPrinter()">{{ t('pos.printers.new') }}</Button>
        </header>
        <p class="m-0 mb-2 text-xs text-muted-foreground">{{ t('pos.printers.agentNote') }}</p>
        <p v-if="devices.length === 0 && printers.length > 0" class="m-0 mb-2 rounded-lg border border-dashed border-border p-2 text-xs text-muted-foreground" data-testid="printers-no-agent">{{ t('pos.printers.noAgent') }}</p>
        <p v-if="printers.length === 0" class="muted" data-testid="printers-empty">{{ t('pos.printers.empty') }}</p>
        <ul class="m-0 list-none divide-y divide-border p-0">
          <li v-for="p in printers" :key="p.id" class="flex min-h-11 flex-wrap items-center gap-2 py-2" :data-testid="`printer-${p.id}`">
            <div class="min-w-0 flex-1">
              <strong class="block truncate text-sm">{{ p.name }}</strong>
              <span class="text-xs text-muted-foreground">{{ t(`pos.printers.kind_${p.kind}`) }} · {{ p.paper_width_mm }} mm<template v-if="p.connection_info"> · {{ p.connection_info }}</template><template v-if="p.device_name"> · {{ p.device_name }}</template></span>
            </div>
            <Badge v-if="!p.is_active" variant="outline">{{ t('common.inactive') }}</Badge>
            <Badge v-else :variant="p.state === 'ONLINE' ? 'success' : p.state === 'UNKNOWN' || p.state === 'NO_DEVICE' ? 'outline' : 'destructive'" :data-testid="`printer-state-${p.id}`" :data-state="p.state">{{ t(`pos.printers.state_${p.state ?? 'NO_DEVICE'}`) }}</Badge>
            <Button variant="outline" size="sm" @click="editPrinter(p)">{{ t('common.edit') }}</Button>
          </li>
        </ul>
      </section>
    </div>

    <section class="mt-4 rounded-xl border border-border bg-card p-4" data-testid="print-queue">
      <header class="mb-1 flex flex-wrap items-baseline justify-between gap-2">
        <h2 class="m-0 text-base font-semibold">{{ t('posPrintQueue.title') }}</h2>
        <span v-if="queue && Object.values(queue.queued).some((n) => n > 0)" class="text-xs text-muted-foreground" data-testid="queue-depth">
          {{ t('posPrintQueue.queued', { n: Object.values(queue.queued).reduce((a, b) => a + b, 0) }) }}
        </span>
      </header>
      <p class="m-0 mb-3 text-xs text-muted-foreground">{{ t('posPrintQueue.intro') }}</p>
      <div v-if="!queue || queue.jobs.length === 0" class="rounded-lg border border-dashed border-border p-6 text-center" data-testid="queue-empty">
        <p class="m-0 text-sm font-medium">{{ t('posPrintQueue.empty') }}</p>
        <p class="m-0 mt-1 text-xs text-muted-foreground">{{ t('posPrintQueue.emptyHint') }}</p>
      </div>
      <table v-else class="w-full text-sm">
        <thead><tr class="text-left text-xs text-muted-foreground"><th>{{ t('posPrintQueue.what') }}</th><th>{{ t('posPrintQueue.printer') }}</th><th>{{ t('posPrintQueue.device') }}</th><th>{{ t('posPrintQueue.at') }}</th><th>{{ t('posPrintQueue.attempts') }}</th><th>{{ t('posPrintQueue.printedAt') }}</th><th>{{ t('posPrintQueue.state') }}</th></tr></thead>
        <tbody>
          <tr v-for="j in queue.jobs" :key="j.id" class="border-t border-border" :data-testid="`job-${j.id}`" :data-status="j.status">
            <td>
              {{ j.kind !== 'TICKET' ? t(`posPrintStatus.kind_${j.kind}`) : j.ticket_kind ? t(`posPrintQueue.ticket_${j.ticket_kind}`) : t('posPrintQueue.ticket') }}
              <span class="text-xs text-muted-foreground">{{ [j.station, j.bill_number, j.table_code].filter(Boolean).join(' · ') }}</span>
            </td>
            <td>{{ j.printer_name }}</td>
            <td>{{ j.device_name || '—' }}</td>
            <td class="tabular-nums">{{ formatDateTime(j.created_at) }}</td>
            <td class="tabular-nums">{{ j.attempts }}/{{ j.max_attempts ?? 3 }}</td>
            <td class="tabular-nums">{{ j.printed_at ? formatDateTime(j.printed_at) : '—' }}</td>
            <td>
              <Badge :variant="j.status === 'FAILED' ? 'destructive' : 'outline'">{{ t(`posPrintQueue.status_${j.status}`) }}</Badge>
              <span v-if="j.is_reprint" class="ml-1 text-xs text-muted-foreground">{{ t('posPrintQueue.copy', { n: j.copy_no }) }}</span>
              <span v-if="j.error_code" class="block text-xs text-destructive" data-testid="job-error">{{ jobError(j.error_code) }}</span>
            </td>
          </tr>
        </tbody>
      </table>
    </section>
  </template>

  <Dialog :open="sDialog" @update:open="(v) => (sDialog = v)">
    <DialogContent class="max-w-lg p-5" data-testid="station-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="saveStation">
        <DialogTitle>{{ sForm.id === null ? t('pos.stations.newStation') : t('pos.stations.editStation') }}</DialogTitle>
        <DialogDescription>{{ t('pos.stations.stationHint') }}</DialogDescription>
        <ErrorNotice v-if="sError" :error="sError" inline />
        <FormField :label="t('pos.stations.code')" :error="sError?.fieldMessage('code')">
          <template #default="{ id }"><Input :id="id" v-model="sForm.code" name="code" maxlength="20" :disabled="sForm.id !== null" class="uppercase" /></template>
        </FormField>
        <FormField :label="t('pos.stations.name')" :error="sError?.fieldMessage('name')">
          <template #default="{ id }"><Input :id="id" v-model="sForm.name" name="name" maxlength="100" /></template>
        </FormField>
        <FormField :label="t('pos.stations.printer')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model.number="sForm.printer_id" name="printer_id">
              <option :value="0">{{ t('pos.stations.noPrinter') }}</option>
              <option v-for="p in printers.filter((x) => x.is_active)" :key="p.id" :value="p.id">{{ p.name }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <FormField :label="t('pos.stations.ticketCopies')" :hint="t('pos.stations.ticketCopiesHint')" :error="sError?.fieldMessage('ticket_copies')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model.number="sForm.ticket_copies" name="ticket_copies" data-testid="ticket-copies"><option v-for="n in [1, 2, 3, 4, 5]" :key="n" :value="n">{{ n }}</option></NativeSelect>
          </template>
        </FormField>
        <FormField :label="t('pos.stations.lateAfterLabel')" :error="sError?.fieldMessage('late_after_minutes')">
          <template #default="{ id }"><Input :id="id" v-model.number="sForm.late_after_minutes" name="late_after_minutes" type="number" min="1" max="240" class="max-w-28" /></template>
        </FormField>
        <label class="flex min-h-11 cursor-pointer items-center gap-2 text-sm"><input v-model="sForm.kds_enabled" type="checkbox" name="kds_enabled" class="size-4 accent-primary" />{{ t('pos.stations.kdsLabel') }}</label>
        <label class="flex min-h-11 cursor-pointer items-center gap-2 text-sm"><input v-model="sForm.is_active" type="checkbox" name="is_active" class="size-4 accent-primary" />{{ t('common.active') }}</label>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="sDialog = false">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy || !sForm.name.trim() || !sForm.code.trim()" data-testid="station-save">{{ t('common.save') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>

  <Dialog :open="pDialog" @update:open="(v) => (pDialog = v)">
    <DialogContent class="max-w-lg p-5" data-testid="printer-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="savePrinter">
        <DialogTitle>{{ pForm.id === null ? t('pos.printers.new') : t('pos.printers.edit') }}</DialogTitle>
        <ErrorNotice v-if="pError" :error="pError" inline />
        <FormField :label="t('pos.printers.name')" :error="pError?.fieldMessage('name')">
          <template #default="{ id }"><Input :id="id" v-model="pForm.name" name="name" maxlength="100" /></template>
        </FormField>
        <div class="grid grid-cols-2 gap-3">
          <FormField :label="t('pos.printers.kind')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="pForm.kind" name="kind">
                <option v-for="k in ['RECEIPT', 'KITCHEN', 'LABEL']" :key="k" :value="k">{{ t(`pos.printers.kind_${k}`) }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('pos.printers.width')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model.number="pForm.paper_width_mm" name="paper_width_mm"><option :value="58">58 mm</option><option :value="80">80 mm</option></NativeSelect>
            </template>
          </FormField>
        </div>
        <FormField :label="t('pos.printers.device')" :hint="t('pos.printers.deviceHint')" :error="pError?.fieldMessage('device_id')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model.number="pForm.device_id" name="device_id" data-testid="printer-device">
              <option :value="0">{{ t('pos.printers.noDevice') }}</option>
              <option v-for="d in devices.filter((x) => x.status === 'ACTIVE' || x.id === pForm.device_id)" :key="d.id" :value="d.id">{{ d.name }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <FormField :label="t('pos.printers.connection')" :hint="t('pos.printers.connectionHint')">
          <template #default="{ id }"><Input :id="id" v-model="pForm.connection_info" name="connection_info" maxlength="200" /></template>
        </FormField>
        <FormField :label="t('pos.printers.codepage')">
          <template #default="{ id }"><Input :id="id" v-model="pForm.codepage" name="codepage" maxlength="20" class="max-w-40" /></template>
        </FormField>
        <label class="flex min-h-11 cursor-pointer items-center gap-2 text-sm"><input v-model="pForm.opens_drawer" type="checkbox" name="opens_drawer" class="size-4 accent-primary" />{{ t('pos.printers.opensDrawer') }}</label>
        <label class="flex min-h-11 cursor-pointer items-center gap-2 text-sm"><input v-model="pForm.is_active" type="checkbox" name="is_active" class="size-4 accent-primary" />{{ t('common.active') }}</label>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="pDialog = false">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy || !pForm.name.trim()" data-testid="printer-save">{{ t('common.save') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
