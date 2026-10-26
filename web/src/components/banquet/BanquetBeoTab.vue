<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { BanquetBeo, BanquetBeoChange, BanquetBeoRevision, BanquetBeoSection, BanquetBeoSession, BanquetEvent } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { toast } from '@/composables/useToast'
import { t } from '@/i18n'
import { documentPath, openPdf } from '@/utils/documents'
import { formatDateShort, formatDateTime } from '@/utils/format'

/**
 * The tab "BEO" (docs/architecture/36-banquet.md 4.6, mockup 42): for each session the text of each department (kitchen, F&B service, housekeeping, engineering, security, front office), the run of the day, and
 * where each section stands: unread, read, or changed since the revision that was read. Before Definite the BEO is a draft; after it every save of the event is a revision, kept with its snapshot, and the head
 * and the deputy of the department of a section that changed are told. A section whose department the property did not map tells nobody, and says so.
 */
const props = defineProps<{ propertyId: number; event: BanquetEvent; canManage: boolean }>()
const emit = defineEmits<{ changed: [] }>()

const beo = ref<BanquetBeo | null>(null)
const error = ref<ApiError | null>(null)
const busy = ref(false)
// what is typed, by session id then section; the run of the day by session id
const texts = ref<Record<number, Record<string, string>>>({})
const runs = ref<Record<number, { at_time: string; text: string }[]>>({})
const notes = ref<Record<number, string>>({})
const dirty = ref<Record<number, boolean>>({})

const editable = computed(() => props.canManage && ['INQUIRY', 'TENTATIVE', 'DEFINITE'].includes(props.event.status))
const sessions = computed(() => (beo.value?.sessions ?? []).filter((s) => s.status === 'ACTIVE'))

function fill(b: BanquetBeo): void {
  beo.value = b
  const tx: Record<number, Record<string, string>> = {}
  const rn: Record<number, { at_time: string; text: string }[]> = {}
  for (const s of b.sessions) {
    tx[s.id] = Object.fromEntries(s.sections.map((x) => [x.section, x.body]))
    rn[s.id] = s.run_of_show.map((r) => ({ at_time: r.at_time.slice(0, 5), text: r.text }))
  }
  texts.value = tx
  runs.value = rn
  notes.value = {}
  dirty.value = {}
}

async function load(): Promise<void> {
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/banquet/events/{id}/beo', { params: { path: { propertyId: props.propertyId, id: props.event.id } } })
    if (data) fill(data)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
watch(() => [props.propertyId, props.event.id, props.event.status], load, { immediate: true })

async function save(s: BanquetBeoSession): Promise<void> {
  busy.value = true
  error.value = null
  try {
    const { data } = await api.PUT('/api/v1/properties/{propertyId}/banquet/events/{id}/sessions/{sid}/beo', {
      params: { path: { propertyId: props.propertyId, id: props.event.id, sid: s.id } },
      body: {
        sections: texts.value[s.id] ?? {},
        run_of_show: (runs.value[s.id] ?? []).filter((r) => r.at_time && r.text.trim()).map((r) => ({ at_time: r.at_time, text: r.text.trim() })),
        note: notes.value[s.id]?.trim() || null,
      },
    })
    if (data) fill(data)
    toast.success(t('banquet.beo.saved'))
    emit('changed')
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function ack(s: BanquetBeoSession, section: BanquetBeoSection['section']): Promise<void> {
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/banquet/events/{id}/beo/ack', { params: { path: { propertyId: props.propertyId, id: props.event.id } }, body: { session_id: s.id, section } })
    if (data) {
      // only the marks change: what is typed and not saved stays
      const keep = texts.value
      const keepRuns = runs.value
      const keepDirty = dirty.value
      fill(data)
      texts.value = keep
      runs.value = keepRuns
      dirty.value = keepDirty
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

function addRun(s: BanquetBeoSession): void {
  runs.value[s.id] = [...(runs.value[s.id] ?? []), { at_time: '', text: '' }]
  dirty.value[s.id] = true
}
function dropRun(s: BanquetBeoSession, i: number): void {
  runs.value[s.id] = (runs.value[s.id] ?? []).filter((_, j) => j !== i)
  dirty.value[s.id] = true
}

const stateVariant = (x: BanquetBeoSection): 'draft' | 'booked' | 'inhouse' | 'destructive' =>
  x.state === 'CHANGED' ? 'destructive' : x.state === 'READ' ? 'inhouse' : x.state === 'UNREAD' ? 'booked' : 'draft'
const stateText = (x: BanquetBeoSection): string => (x.state === 'CHANGED' ? t('banquet.beo.state.CHANGED', { n: x.changed_since ?? 0 }) : t(`banquet.beo.state.${x.state}`, { n: x.read_revision ?? 0 }))
const range = (s: BanquetBeoSession): string => (s.start ? `${s.start.slice(0, 5)}–${(s.end ?? '').slice(0, 5)}${s.ends_next_day ? ' +1' : ''}` : '–')

// ---- the history
const view = ref<BanquetBeoRevision | null>(null)
const viewOpen = ref(false)
async function showRevision(no: number): Promise<void> {
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/banquet/events/{id}/beo/revisions/{no}', { params: { path: { propertyId: props.propertyId, id: props.event.id, no } } })
    view.value = data ?? null
    viewOpen.value = !!data
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
// a set-up is named as the rest of the app names it, never by its code
const styleName = (code: string): string => (code ? t(`banquet.style.${code}`) : '')
// one entry for each person: the last revision that person read
function readers(x: BanquetBeoSection): string {
  const last = new Map<string, number>()
  for (const r of x.read_by) last.set(r.user_name, Math.max(last.get(r.user_name) ?? 0, r.revision))
  return [...last].map(([name, revision]) => `${name} (r${revision})`).join(', ')
}
// a section that is told to nobody: no department, or a department that has neither a head nor a deputy
const nobodyTold = (x: BanquetBeoSection): boolean => x.no_department || (!x.head && !x.deputy)
function sentence(c: BanquetBeoChange): string {
  const shown = (v: string): string => (c.field === 'setup' && v ? styleName(v) : v || '–')
  const p = { no: c.session_no ?? '', field: c.field ? t(`banquet.beo.field.${c.field}`) : '', section: c.section ? t(`banquet.beo.section.${c.section}`) : '', old: shown(c.old ?? ''), new: shown(c.new ?? ''), text: c.text ?? '' }
  return t(`banquet.beo.change.${c.kind}`, p)
}
async function pdf(path: string): Promise<void> {
  error.value = null
  try {
    await openPdf(path)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="beo-tab">
    <ErrorNotice v-if="error" :error="error" inline />
    <div v-if="beo" class="flex flex-wrap items-center justify-between gap-2">
      <p class="m-0 text-sm" data-testid="beo-revision">
        <Badge :variant="beo.draft ? 'draft' : 'inhouse'">{{ beo.draft ? t('banquet.beo.draft') : t('banquet.beo.revision', { n: beo.revision }) }}</Badge>
        <span class="ml-2 text-muted-foreground">{{ beo.draft ? t('banquet.beo.draftHint') : t('banquet.beo.revisionHint') }}</span>
      </p>
      <Button type="button" variant="outline" size="sm" data-testid="beo-pdf" @click="pdf(documentPath.banquetBeo(propertyId, event.id))">{{ t('banquet.beo.pdfAll') }}</Button>
    </div>

    <p v-if="beo && !sessions.length" class="m-0 text-sm text-muted-foreground">{{ t('banquet.beo.noSessions') }}</p>

    <Card v-for="s in sessions" :key="s.id" class="p-4" :data-testid="`beo-session-${s.id}`">
      <div class="mb-3 flex flex-wrap items-start justify-between gap-2">
        <div class="min-w-0">
          <h3 class="m-0 text-base font-semibold">{{ s.no }}. {{ s.name }}</h3>
          <p class="m-0 text-sm text-muted-foreground">
            {{ formatDateShort(s.date) }} · {{ range(s) }} · {{ s.room || t('banquet.session.noRoom') }}<template v-if="s.setup"> · {{ styleName(s.setup) }}</template> · {{ t('banquet.session.paxCount', { n: s.pax_guaranteed ?? s.pax_estimated }) }}
          </p>
        </div>
        <Button type="button" variant="outline" size="sm" :data-testid="`beo-pdf-${s.id}`" @click="pdf(documentPath.banquetBeo(propertyId, event.id, s.id))">{{ t('banquet.beo.pdfSession') }}</Button>
      </div>

      <div class="flex flex-col gap-3">
        <section v-for="x in s.sections" :key="x.section" class="rounded-md border border-border p-3" :data-testid="`beo-${s.id}-${x.section}`">
          <div class="mb-1 flex flex-wrap items-center justify-between gap-2">
            <h4 class="m-0 text-sm font-semibold">{{ t(`banquet.beo.section.${x.section}`) }}</h4>
            <div class="flex flex-wrap items-center gap-2">
              <Badge :variant="stateVariant(x)" :data-testid="`beo-state-${s.id}-${x.section}`">{{ stateText(x) }}</Badge>
              <Button v-if="!beo?.draft && x.section !== 'GENERAL' && x.state !== 'READ'" type="button" variant="outline" size="sm" :data-testid="`beo-ack-${s.id}-${x.section}`" @click="ack(s, x.section)">{{ t('banquet.beo.markRead') }}</Button>
            </div>
          </div>
          <p v-if="x.section === 'GENERAL'" class="m-0 mb-1 text-xs text-muted-foreground">{{ t('banquet.beo.generalHint') }}</p>
          <p v-else-if="nobodyTold(x)" class="m-0 mb-1 text-xs text-destructive" :data-testid="`beo-nodept-${s.id}-${x.section}`">{{ x.no_department ? t('banquet.beo.noDepartment') : t('banquet.beo.noHead', { department: x.department_name ?? '' }) }}</p>
          <p v-else class="m-0 mb-1 text-xs text-muted-foreground">
            {{ t('banquet.beo.told', { department: x.department_name ?? '', head: x.head ?? '–', deputy: x.deputy ?? '–' }) }}
          </p>
          <textarea
            v-if="editable"
            v-model="texts[s.id]![x.section]"
            :name="`beo_${x.section}`"
            rows="3"
            maxlength="4000"
            class="w-full rounded-md border border-border bg-card p-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
            :data-testid="`beo-text-${s.id}-${x.section}`"
            @input="dirty[s.id] = true"
          />
          <p v-else class="m-0 whitespace-pre-wrap text-sm">{{ x.body || '–' }}</p>
          <p v-if="x.read_by.length" class="m-0 mt-1 text-xs text-muted-foreground">{{ t('banquet.beo.readBy', { names: readers(x) }) }}</p>
        </section>

        <section class="rounded-md border border-border p-3" :data-testid="`beo-run-${s.id}`">
          <h4 class="m-0 mb-1 text-sm font-semibold">{{ t('banquet.beo.runOfShow') }}</h4>
          <ul v-if="editable" class="m-0 flex list-none flex-col gap-2 p-0">
            <li v-for="(r, i) in runs[s.id] ?? []" :key="i" class="flex items-center gap-2">
              <Input v-model="r.at_time" type="time" name="at_time" class="w-28" @input="dirty[s.id] = true" />
              <Input v-model="r.text" name="run_text" maxlength="300" @input="dirty[s.id] = true" />
              <Button type="button" variant="outline" size="sm" @click="dropRun(s, i)">{{ t('common.remove') }}</Button>
            </li>
          </ul>
          <ul v-else class="m-0 list-none p-0 text-sm">
            <li v-for="r in s.run_of_show" :key="r.seq"><span class="tabular-nums text-muted-foreground">{{ r.at_time.slice(0, 5) }}</span> {{ r.text }}</li>
            <li v-if="!s.run_of_show.length">–</li>
          </ul>
          <Button v-if="editable" type="button" variant="outline" size="sm" class="mt-2" :data-testid="`beo-run-add-${s.id}`" @click="addRun(s)">{{ t('banquet.beo.addRun') }}</Button>
        </section>

        <div v-if="editable" class="flex flex-wrap items-center justify-end gap-2">
          <Input v-if="!beo?.draft" v-model="notes[s.id]" name="note" maxlength="300" class="max-w-xs" :placeholder="t('banquet.beo.notePlaceholder')" />
          <Button type="button" :disabled="busy || !dirty[s.id]" :data-testid="`beo-save-${s.id}`" @click="save(s)">{{ beo?.draft ? t('common.save') : t('banquet.beo.saveRevision') }}</Button>
        </div>
      </div>
    </Card>

    <Card v-if="beo" class="p-4" data-testid="beo-history">
      <h3 class="m-0 mb-2 text-base font-semibold">{{ t('banquet.beo.history') }}</h3>
      <p v-if="!beo.revisions.length" class="m-0 text-sm text-muted-foreground">{{ t('banquet.beo.historyEmpty') }}</p>
      <ul v-else class="m-0 flex list-none flex-col gap-3 p-0">
        <li v-for="r in beo.revisions" :key="r.revision_no" class="rounded-md border border-border p-3 text-sm" :data-testid="`revision-${r.revision_no}`">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <strong>{{ t('banquet.beo.revision', { n: r.revision_no }) }}</strong>
            <span class="text-muted-foreground">{{ formatDateTime(r.created_at) }} · {{ r.created_by_name }}</span>
          </div>
          <p v-if="r.note" class="m-0 mt-1">{{ r.note }}</p>
          <ul class="m-0 mt-1 list-disc pl-5 text-muted-foreground">
            <li v-for="(c, i) in r.changes.slice(0, 8)" :key="i">{{ sentence(c) }}</li>
            <li v-if="r.changes.length > 8">{{ t('banquet.beo.moreChanges', { n: r.changes.length - 8 }) }}</li>
          </ul>
          <p class="m-0 mt-1 text-xs text-muted-foreground">{{ t('banquet.beo.notified', { n: r.notified }) }}</p>
          <div class="mt-2 flex gap-2">
            <Button type="button" variant="outline" size="sm" :data-testid="`revision-view-${r.revision_no}`" @click="showRevision(r.revision_no)">{{ t('banquet.beo.viewRevision') }}</Button>
            <Button type="button" variant="outline" size="sm" @click="pdf(documentPath.banquetBeo(propertyId, event.id, undefined, r.revision_no))">{{ t('banquet.beo.pdfRevision') }}</Button>
          </div>
        </li>
      </ul>
    </Card>

    <Dialog :open="viewOpen" @update:open="(v) => (viewOpen = v)">
      <DialogContent class="max-h-[90dvh] max-w-2xl overflow-y-auto p-5" data-testid="revision-dialog">
        <DialogTitle>{{ t('banquet.beo.revisionOf', { n: view?.revision_no ?? 0 }) }}</DialogTitle>
        <DialogDescription>{{ t('banquet.beo.revisionOfHint') }}</DialogDescription>
        <div v-for="s in view?.snapshot.sessions ?? []" :key="s.id" class="mt-2 text-sm">
          <h4 class="m-0 font-semibold">{{ s.no }}. {{ s.name }} · {{ formatDateShort(s.date) }} · {{ s.room || '–' }}<template v-if="s.setup"> · {{ styleName(s.setup) }}</template> · {{ t('banquet.session.paxCount', { n: s.pax_guaranteed ?? s.pax_estimated }) }}</h4>
          <template v-for="(body, section) in s.sections" :key="section">
            <p v-if="body" class="m-0 mt-1 whitespace-pre-wrap"><strong>{{ t(`banquet.beo.section.${section}`) }}:</strong> {{ body }}</p>
          </template>
        </div>
        <div class="mt-3 flex justify-end"><Button type="button" variant="outline" @click="viewOpen = false">{{ t('common.close') }}</Button></div>
      </DialogContent>
    </Dialog>
  </div>
</template>
