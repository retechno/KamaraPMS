<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { Search } from 'lucide-vue-next'
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { HousekeepingBoardRoom, LostFoundItem, LostFoundOwner } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Combobox } from '@/components/ui/combobox'
import { NativeSelect } from '@/components/ui/native-select'
import { vAutofocus } from '@/directives/autofocus'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const rows = ref<LostFoundItem[]>([])
const nextCursor = ref<string | undefined>()
const roomList = ref<HousekeepingBoardRoom[]>([])
const owners = ref<LostFoundOwner[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const creating = ref(false)
const selectedId = ref<number | null>(null)
const filter = reactive({ status: 'STORED', category: '', q: '' })
const form = reactive({ room_id: 0, location: '', category: 'OTHER', description: '', storage_location: '', possible_owner: '', notes: '' })
const closing = ref<'return' | 'dispose' | null>(null)
const handBack = reactive({ claimant_name: '', claimant_proof: '', note: '' })
const disposeReason = ref('')
const storage = ref('')

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const selected = computed(() => rows.value.find((r) => r.id === selectedId.value) ?? null)
const fieldError = (field: string) => error.value?.fieldMessage(field)
const CATEGORIES = ['ELECTRONICS', 'CLOTHING', 'DOCUMENTS', 'JEWELRY', 'BAGS', 'OTHER']
const where = (i: LostFoundItem) => (i.room_number ? t('lostFound.roomWord', { number: i.room_number }) : i.location || '')
const categoryLabel = (c: string): string => t(`lostFound.cat${c}` as never)

const columns = computed<Column<LostFoundItem>[]>(() => [
  { key: 'item_number', label: t('lostFound.number'), sortable: true },
  { key: 'found_on', label: t('lostFound.found'), sortable: true, format: 'date' as const },
  { key: 'where', label: t('lostFound.where') },
  { key: 'description', label: t('lostFound.item') },
  { key: 'storage_location', label: t('lostFound.keptAt') },
  { key: 'status', label: t('lostFound.status'), sortable: true },
])

async function load(more = false): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('lostfound.report')) return
  try {
    const query: Record<string, unknown> = { limit: 50, cursor: more ? nextCursor.value : undefined }
    if (filter.status) query.status = filter.status
    if (filter.category) query.category = filter.category
    if (filter.q.trim()) query.q = filter.q.trim()
    const { data } = await api.GET('/api/v1/properties/{propertyId}/lost-found', { params: { path: { propertyId }, query: query as never } })
    rows.value = more ? [...rows.value, ...(data?.data ?? [])] : (data?.data ?? [])
    nextCursor.value = data?.next_cursor
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

async function loadRooms(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/housekeeping', { params: { path: { propertyId } } })
    roomList.value = data?.data ?? []
  } catch {
    roomList.value = []
  }
}

// One call; the list is reloaded also after a refusal (another desk may have closed the item).
async function run(what: () => Promise<unknown>, done: string): Promise<boolean> {
  busy.value = true
  error.value = null
  notice.value = ''
  let failure: ApiError | null = null
  try {
    await what()
    notice.value = done
  } catch (e) {
    failure = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
  await load()
  error.value = failure
  return !failure
}

const base = () => ({ path: { propertyId: pid.value as number, id: selectedId.value as number } })

function startNew(): void {
  Object.assign(form, { room_id: 0, location: '', category: 'OTHER', description: '', storage_location: '', possible_owner: '', notes: '' })
  creating.value = true
  error.value = null
}

async function record(): Promise<void> {
  const ok = await run(async () => {
    await api.POST('/api/v1/properties/{propertyId}/lost-found', {
      params: { path: { propertyId: pid.value as number } },
      body: {
        room_id: form.room_id || null, location: form.location || undefined, category: form.category as 'OTHER', description: form.description,
        storage_location: form.storage_location || undefined, possible_owner: form.possible_owner || undefined, notes: form.notes || undefined,
      },
    })
  }, t('lostFound.noticeRecorded'))
  if (ok) creating.value = false
}

async function select(i: LostFoundItem): Promise<void> {
  selectedId.value = i.id
  closing.value = null
  storage.value = i.storage_location ?? ''
  Object.assign(handBack, { claimant_name: i.possible_owner ?? '', claimant_proof: '', note: '' })
  disposeReason.value = ''
  owners.value = []
  error.value = null
  if (i.room_id && can('reservation.read') && pid.value !== null) {
    try {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/lost-found/{id}/possible-owners', { params: base() })
      owners.value = data?.data ?? []
    } catch {
      owners.value = []
    }
  }
}

const saveStorage = () => run(() => api.PATCH('/api/v1/properties/{propertyId}/lost-found/{id}', { params: base(), body: { storage_location: storage.value } }), t('lostFound.noticeSaved'))

async function finish(): Promise<void> {
  const how = closing.value
  if (!how) return
  const ok = await run(
    () => (how === 'return'
      ? api.POST('/api/v1/properties/{propertyId}/lost-found/{id}/return', { params: base(), body: { claimant_name: handBack.claimant_name, claimant_proof: handBack.claimant_proof || undefined, note: handBack.note || undefined } })
      : api.POST('/api/v1/properties/{propertyId}/lost-found/{id}/dispose', { params: base(), body: { reason: disposeReason.value } })),
    how === 'return' ? t('lostFound.noticeHandedBack') : t('lostFound.noticeDisposed'),
  )
  if (ok) closing.value = null
}

function useOwner(o: LostFoundOwner): void {
  handBack.claimant_name = o.guest_name
  handBack.claimant_proof = t('lostFound.stayProof', { number: o.stay_number })
  closing.value = 'return'
}

watch(() => pid.value, () => {
  rows.value = []
  loaded.value = false
  selectedId.value = null
  void load()
  void loadRooms()
}, { immediate: true })
watch(() => [filter.status, filter.category], () => void load())
</script>

<template>
  <PageHeader :title="t('lostFound.title')">
    <template #actions>
      <Button v-if="can('lostfound.report') && !creating" size="sm" data-testid="new-item" @click="startNew">{{ t('lostFound.record') }}</Button>
    </template>
  </PageHeader>

  <ErrorNotice v-if="error" :error="error" inline data-testid="lf-error" />
  <p v-if="notice" class="alert warning" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('lostFound.selectProperty') }}</p>
  <p v-else-if="!can('lostfound.report')" class="muted" data-testid="no-access">{{ t('lostFound.noAccess') }}</p>

  <template v-else>
    <Card v-if="creating" class="mb-4 border-primary/50">
      <form v-autofocus novalidate data-testid="item-form" @submit.prevent="record">
        <CardHeader><CardTitle>{{ t('lostFound.record') }}</CardTitle></CardHeader>
        <CardContent>
          <div class="grid gap-4 sm:grid-cols-2">
            <FormField :label="t('lostFound.foundInRoom')">
              <template #default="{ id }">
                <Combobox :id="id" v-model="form.room_id" name="room_id" :options="[{ value: 0, label: `${t('lostFound.notInRoom')}` }, ...roomList.map((r) => ({ value: r.room_id, label: `${r.room_number} · ${r.room_type_code}` }))]" />
              </template>
            </FormField>
            <FormField :label="t('lostFound.place')" :error="fieldError('location')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.location" name="location" maxlength="150" :placeholder="t('lostFound.placePlaceholder')" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('lostFound.category')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model="form.category" name="category">
                  <option v-for="c in CATEGORIES" :key="c" :value="c">{{ categoryLabel(c) }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('lostFound.keptAt')">
              <template #default="{ id }"><Input :id="id" v-model="form.storage_location" name="storage_location" maxlength="100" :placeholder="t('lostFound.keptPlaceholder')" /></template>
            </FormField>
            <FormField class="sm:col-span-2" :label="t('lostFound.whatFound')" :error="fieldError('description')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.description" name="description" maxlength="500" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('lostFound.possibleOwner')">
              <template #default="{ id }"><Input :id="id" v-model="form.possible_owner" name="possible_owner" maxlength="150" /></template>
            </FormField>
            <FormField :label="t('lostFound.notes')">
              <template #default="{ id }"><Input :id="id" v-model="form.notes" name="notes" maxlength="500" /></template>
            </FormField>
          </div>
          <div class="mt-4 flex justify-end gap-2">
            <Button type="button" variant="outline" @click="creating = false">{{ t('common.cancel') }}</Button>
            <Button type="submit" :disabled="busy">{{ t('lostFound.recordButton') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>

    <div :class="cn('grid items-start gap-4', selected && 'xl:grid-cols-[minmax(0,1fr)_26rem]')">
      <div class="min-w-0">
        <form class="mb-3 flex flex-wrap items-end gap-3" novalidate data-testid="filters" @submit.prevent="load()">
          <FormField class="w-40" :label="t('lostFound.show')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="filter.status" name="status">
                <option value="STORED">{{ t('lostFound.stSTORED') }}</option>
                <option value="">{{ t('lostFound.everything') }}</option>
                <option value="RETURNED">{{ t('lostFound.stRETURNED') }}</option>
                <option value="DISPOSED">{{ t('lostFound.stDISPOSED') }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField class="w-44" :label="t('lostFound.category')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="filter.category" name="filter_category">
                <option value="">{{ t('lostFound.any') }}</option>
                <option v-for="c in CATEGORIES" :key="c" :value="c">{{ categoryLabel(c) }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField class="min-w-48 flex-1" :label="t('lostFound.search')">
            <template #default="{ id }"><Input :id="id" v-model="filter.q" name="q" type="search" :placeholder="t('lostFound.searchPlaceholder')" /></template>
          </FormField>
          <Button type="submit" variant="outline"><Search />{{ t('lostFound.search') }}</Button>
        </form>

        <DataTable
          :columns="columns"
          :rows="rows"
          row-key="id"
          :loading="!loaded"
          :row-test-id="(i) => `item-${i.item_number}`"
          :row-class="(i) => cn(i.id === selectedId && 'bg-accent', i.status !== 'STORED' && 'closed text-muted-foreground')"
          :caption="t('lostFound.title')"
          data-testid="items"
        >
          <template #cell-item_number="{ row: i }">
            <button type="button" class="cursor-pointer border-0 bg-transparent p-0 text-primary underline-offset-2 hover:underline" :data-testid="`select-${i.item_number}`" @click="select(i)">{{ i.item_number }}</button>
          </template>
          <template #cell-where="{ row: i }">{{ where(i) }}</template>
          <template #cell-description="{ row: i }">{{ i.description }} <small class="text-muted-foreground">{{ categoryLabel(i.category) }}</small></template>
          <template #cell-status="{ row: i }">
            <Badge :variant="i.status === 'STORED' ? 'default' : i.status === 'RETURNED' ? 'success' : 'outline'">{{ t(`lostFound.st${i.status}` as never) }}</Badge>
            <small v-if="i.claimant_name" class="ml-1 text-muted-foreground">{{ i.claimant_name }}</small>
          </template>
          <template #empty><EmptyState :title="t('lostFound.empty')" data-testid="empty" /></template>
          <template #footer>
            <div v-if="nextCursor" class="flex justify-center p-3"><Button variant="outline" size="sm" data-testid="more" @click="load(true)">{{ t('lostFound.loadMore') }}</Button></div>
          </template>
        </DataTable>
      </div>

      <Card v-if="selected" data-testid="detail">
        <CardHeader><CardTitle>{{ selected.item_number }} · {{ selected.description }}</CardTitle></CardHeader>
        <CardContent class="flex flex-col gap-4">
          <p class="m-0 text-sm text-muted-foreground">
            {{ t('lostFound.foundLine', { date: $date(selected.found_on) }) }}{{ where(selected) ? ` ${t('lostFound.inPlace', { place: where(selected) })}` : '' }}{{ selected.finder_name ? ` ${t('lostFound.byFinder', { name: selected.finder_name })}` : '' }}.
            <template v-if="selected.possible_owner"> {{ t('lostFound.possibleOwnerLine', { name: selected.possible_owner }) }}</template>
            <template v-if="selected.status === 'RETURNED'"> {{ t('lostFound.handedTo', { name: selected.claimant_name ?? '', date: $date(selected.closed_on) ?? '' }) }}<template v-if="selected.claimant_proof"> ({{ selected.claimant_proof }})</template>.</template>
            <template v-if="selected.status === 'DISPOSED'"> {{ t('lostFound.disposedOn', { date: $date(selected.closed_on) ?? '', note: selected.close_note ?? '' }) }}</template>
          </p>

          <div v-if="owners.length" data-testid="owners">
            <h3 class="m-0 mb-1 text-sm font-semibold">{{ t('lostFound.whoHadRoom') }}</h3>
            <ul class="m-0 flex list-none flex-col gap-2 p-0 text-sm">
              <li v-for="o in owners" :key="o.stay_id" class="flex flex-wrap items-center gap-2">
                <span>{{ o.guest_name }} <small class="text-muted-foreground">{{ t('lostFound.stayLine', { number: o.stay_number, from: $date(o.arrival_date), to: $date(o.departure_date), contact: [o.phone, o.email].filter(Boolean).join(' · ') }) }}</small></span>
                <Button v-if="selected.status === 'STORED' && can('lostfound.manage')" variant="outline" size="sm" :data-testid="`owner-${o.stay_number}`" @click="useOwner(o)">{{ t('lostFound.handToGuest') }}</Button>
              </li>
            </ul>
          </div>

          <template v-if="selected.status === 'STORED' && can('lostfound.manage')">
            <div class="flex flex-wrap items-end gap-3">
              <FormField class="w-48" :label="t('lostFound.keptAt')">
                <template #default="{ id }"><Input :id="id" v-model="storage" name="storage" maxlength="100" /></template>
              </FormField>
              <Button variant="outline" size="sm" :disabled="busy" data-testid="save-storage" @click="saveStorage">{{ t('lostFound.save') }}</Button>
            </div>
            <div class="flex flex-wrap gap-2">
              <Button size="sm" data-testid="return" @click="closing = 'return'">{{ t('lostFound.handBack') }}</Button>
              <Button variant="outline" size="sm" data-testid="dispose" @click="closing = 'dispose'">{{ t('lostFound.dispose') }}</Button>
            </div>

            <form v-if="closing === 'return'" class="flex flex-col gap-3 rounded-lg border border-border bg-muted/40 p-3" novalidate data-testid="return-form" @submit.prevent="finish">
              <FormField :label="t('lostFound.takenBy')" :error="fieldError('claimant_name')">
                <template #default="{ id, invalid }"><Input :id="id" v-model="handBack.claimant_name" name="claimant_name" maxlength="150" :aria-invalid="invalid" /></template>
              </FormField>
              <FormField :label="t('lostFound.checked')">
                <template #default="{ id }"><Input :id="id" v-model="handBack.claimant_proof" name="claimant_proof" maxlength="150" /></template>
              </FormField>
              <FormField :label="t('lostFound.note')">
                <template #default="{ id }"><Input :id="id" v-model="handBack.note" name="note" maxlength="500" /></template>
              </FormField>
              <div class="flex justify-end gap-2">
                <Button type="button" variant="outline" size="sm" @click="closing = null">{{ t('lostFound.back') }}</Button>
                <Button type="submit" size="sm" :disabled="busy || !handBack.claimant_name.trim()" data-testid="return-submit">{{ t('lostFound.confirmHandover') }}</Button>
              </div>
            </form>

            <form v-if="closing === 'dispose'" class="flex flex-col gap-3 rounded-lg border border-border bg-muted/40 p-3" novalidate data-testid="dispose-form" @submit.prevent="finish">
              <FormField :label="t('lostFound.reason')" :error="fieldError('reason')">
                <template #default="{ id, invalid }"><Input :id="id" v-model="disposeReason" name="reason" maxlength="500" :aria-invalid="invalid" /></template>
              </FormField>
              <div class="flex justify-end gap-2">
                <Button type="button" variant="outline" size="sm" @click="closing = null">{{ t('lostFound.back') }}</Button>
                <Button type="submit" variant="destructive" size="sm" :disabled="busy || !disposeReason.trim()" data-testid="dispose-submit">{{ t('lostFound.confirmDisposal') }}</Button>
              </div>
            </form>
          </template>
        </CardContent>
      </Card>
    </div>
  </template>
</template>
