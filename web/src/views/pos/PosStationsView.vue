<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { PosPrinter, PosSetupIssue, PosStation } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'

/**
 * The places of production (kitchen, bar, pastry) and the printers (docs/architecture/34-pos.md 3.2). A station has a printer, a screen of the kitchen display, or both; a printer is a record the
 * print agent of the hotel will find (stage 3): nothing prints from here yet. The configuration check names what an outlet still lacks.
 */
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const stations = ref<PosStation[]>([])
const printers = ref<PosPrinter[]>([])
const issues = ref<PosSetupIssue[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const notice = ref('')

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  if (propertyId === null) return
  try {
    const path = { params: { path: { propertyId } } }
    const [s, p, c] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/pos/stations', path),
      api.GET('/api/v1/properties/{propertyId}/pos/printers', path),
      api.GET('/api/v1/properties/{propertyId}/pos/setup-check', path),
    ])
    stations.value = s.data?.data ?? []
    printers.value = p.data?.data ?? []
    issues.value = c.data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

// ---- stations
const sForm = reactive({ id: null as number | null, code: '', name: '', printer_id: 0, kds_enabled: false, late_after_minutes: 15, is_active: true })
const sDialog = ref(false)
const sError = ref<ApiError | null>(null)
const busy = ref(false)
function editStation(s?: PosStation): void {
  Object.assign(sForm, s
    ? { id: s.id, code: s.code, name: s.name, printer_id: s.printer_id ?? 0, kds_enabled: s.kds_enabled, late_after_minutes: s.late_after_minutes, is_active: s.is_active }
    : { id: null, code: '', name: '', printer_id: 0, kds_enabled: false, late_after_minutes: 15, is_active: true })
  sError.value = null
  sDialog.value = true
}
async function saveStation(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  sError.value = null
  const body = { code: sForm.code.trim().toUpperCase(), name: sForm.name.trim(), printer_id: sForm.printer_id || null, kds_enabled: sForm.kds_enabled, late_after_minutes: sForm.late_after_minutes, is_active: sForm.is_active }
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
const pForm = reactive({ id: null as number | null, name: '', kind: 'RECEIPT', connection_info: '', paper_width_mm: 80, codepage: 'CP437', opens_drawer: false, is_active: true })
const pDialog = ref(false)
const pError = ref<ApiError | null>(null)
function editPrinter(p?: PosPrinter): void {
  Object.assign(pForm, p
    ? { id: p.id, name: p.name, kind: p.kind, connection_info: p.connection_info, paper_width_mm: p.paper_width_mm, codepage: p.codepage, opens_drawer: p.opens_drawer, is_active: p.is_active }
    : { id: null, name: '', kind: 'RECEIPT', connection_info: '', paper_width_mm: 80, codepage: 'CP437', opens_drawer: false, is_active: true })
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
    codepage: pForm.codepage.trim(), opens_drawer: pForm.opens_drawer, is_active: pForm.is_active,
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

const issueText = (i: PosSetupIssue): string => t(`pos.check.${i.code}`, { outlet: i.outlet ?? '', product: i.product ?? '', station: i.station ?? '' })
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
        <li v-for="(i, n) in issues" :key="n" :class="i.severity === 'ERROR' ? 'text-destructive' : 'text-muted-foreground'" :data-code="i.code">{{ issueText(i) }}</li>
      </ul>
    </section>

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
              <span class="text-xs text-muted-foreground">{{ s.code }} · {{ s.printer_name ?? t('pos.stations.noPrinter') }}<template v-if="s.kds_enabled"> · {{ t('pos.stations.kds') }}</template> · {{ t('pos.stations.lateAfter', { n: s.late_after_minutes }) }}</span>
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
        <p v-if="printers.length === 0" class="muted" data-testid="printers-empty">{{ t('pos.printers.empty') }}</p>
        <ul class="m-0 list-none divide-y divide-border p-0">
          <li v-for="p in printers" :key="p.id" class="flex min-h-11 flex-wrap items-center gap-2 py-2" :data-testid="`printer-${p.id}`">
            <div class="min-w-0 flex-1">
              <strong class="block truncate text-sm">{{ p.name }}</strong>
              <span class="text-xs text-muted-foreground">{{ t(`pos.printers.kind_${p.kind}`) }} · {{ p.paper_width_mm }} mm<template v-if="p.connection_info"> · {{ p.connection_info }}</template></span>
            </div>
            <Badge v-if="!p.is_active" variant="outline">{{ t('common.inactive') }}</Badge>
            <Button variant="outline" size="sm" @click="editPrinter(p)">{{ t('common.edit') }}</Button>
          </li>
        </ul>
      </section>
    </div>
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
