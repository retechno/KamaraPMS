<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { ChargeCode, GroupInstructionInput, GroupRouting, PostedCharges } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import GroupMovePosted from '@/components/groups/GroupMovePosted.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { NativeSelect } from '@/components/ui/native-select'
import { confirm } from '@/composables/useConfirm'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

/**
 * The master folio of a group and its routing (docs/architecture/30-group-folio.md, part C): the one bill of the group, and what the room nights of its members do when they are posted: go to the
 * master (all of them, the room nights, some charge codes), except the rooms that stay out. Nights already posted stay where they are; right after the instructions are saved the screen offers to
 * move them with the transfer of part B.
 */
const props = defineProps<{ groupId: number; active: boolean }>()
const emit = defineEmits<{ changed: [] }>()

const auth = useAuthStore()
const property = usePropertyStore()

const routing = ref<GroupRouting | null>(null)
const codes = ref<ChargeCode[]>([])
const posted = ref<PostedCharges[]>([])
const error = ref<ApiError | null>(null)
const busy = ref(false)
const notice = ref('')
const noticeWarns = ref(false) // a notice that says nothing was done is a warning, not a confirmation
const moving = ref(false)

// the form: the instructions as switches, the codes named, the rooms that stay out
const roomOn = ref(false)
const allOn = ref(false)
const picked = ref<number[]>([])
const addCode = ref(0)
const excluded = ref<number[]>([])

const pid = computed(() => property.currentId)
const canRead = computed(() => auth.can('reservation.read', pid.value))
const canManage = computed(() => auth.can('folio.master_manage', pid.value))
const canClose = computed(() => auth.can('folio.master_close', pid.value))
const canFolio = computed(() => auth.can('folio.read', pid.value))
const master = computed(() => routing.value?.master ?? null)
const codeOf = (id: number): ChargeCode | undefined => codes.value.find((c) => c.id === id)
// a code that is manual, active and not the room: the room has its own switch, and TRF is never posted by itself
const choices = computed(() => codes.value.filter((c) => c.is_active && c.charge_type !== 'ROOM' && c.charge_type !== 'TRANSFER' && !picked.value.includes(c.id)))
const dirty = computed(() => {
  const r = routing.value
  if (!r) return false
  const had = new Set(r.instructions.map((i) => i.scope === 'CHARGE_CODE' ? `C${i.charge_code_id}` : i.scope))
  const want = new Set<string>([...(roomOn.value ? ['ROOM'] : []), ...(allOn.value ? ['ALL'] : []), ...picked.value.map((id) => `C${id}`)])
  if (had.size !== want.size || [...want].some((x) => !had.has(x))) return true
  const was = r.lines.filter((l) => l.excluded).map((l) => l.reservation_room_id).sort()
  return JSON.stringify(was) !== JSON.stringify([...excluded.value].sort())
})
// a master with a balance is not closed: the balance is paid, or moved to the city ledger of the company (the server refuses it too)
const owes = computed(() => !!master.value && master.value.balance !== '' && Number(master.value.balance) !== 0)
const postedTotal = computed(() => posted.value.reduce((n, p) => n + p.count, 0))
const postedFolios = computed(() => new Set(posted.value.map((p) => p.folio_id)).size)

function fromRouting(r: GroupRouting): void {
  roomOn.value = r.instructions.some((i) => i.scope === 'ROOM')
  allOn.value = r.instructions.some((i) => i.scope === 'ALL')
  picked.value = r.instructions.filter((i) => i.scope === 'CHARGE_CODE' && i.charge_code_id !== null).map((i) => i.charge_code_id as number)
  excluded.value = r.lines.filter((l) => l.excluded).map((l) => l.reservation_room_id)
}

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !canRead.value) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/groups/{id}/routing', { params: { path: { propertyId, id: props.groupId } } })
    routing.value = data && 'lines' in data ? data : null
    if (routing.value) fromRouting(routing.value)
    if (canManage.value && !codes.value.length) {
      codes.value = await fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/charge-codes', { params: { path: { propertyId }, query: { limit: 200, cursor } } }))
    }
    await loadPosted()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function loadPosted(): Promise<void> {
  const propertyId = pid.value
  posted.value = []
  if (propertyId === null || !canFolio.value || !master.value || master.value.status !== 'OPEN' || !routing.value?.instructions.length) return
  const { data } = await api.GET('/api/v1/properties/{propertyId}/groups/{id}/routing/posted', { params: { path: { propertyId, id: props.groupId } } })
  posted.value = Array.isArray(data?.data) ? data.data : []
}
watch(() => [pid.value, props.groupId], () => void load(), { immediate: true })

function add(): void {
  if (addCode.value > 0 && !picked.value.includes(addCode.value)) picked.value = [...picked.value, addCode.value]
  addCode.value = 0
}

function toggleLine(id: number, on: boolean): void {
  excluded.value = on ? [...new Set([...excluded.value, id])] : excluded.value.filter((x) => x !== id)
}

async function run(action: () => Promise<void>): Promise<void> {
  busy.value = true
  error.value = null
  notice.value = ''
  noticeWarns.value = false
  try {
    await action()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

const openMaster = (): Promise<void> =>
  run(async () => {
    const propertyId = pid.value as number
    const { data } = await api.POST('/api/v1/properties/{propertyId}/groups/{id}/master-folio', { params: { path: { propertyId, id: props.groupId } } })
    notice.value = t('groupMaster.opened', { folio: data?.master.folio_number ?? '' })
    await load()
    emit('changed')
  })

const save = (): Promise<void> =>
  run(async () => {
    const propertyId = pid.value as number
    const instructions: GroupInstructionInput[] = [
      ...(roomOn.value ? [{ scope: 'ROOM' as const }] : []),
      ...(allOn.value ? [{ scope: 'ALL' as const }] : []),
      ...picked.value.map((id) => ({ scope: 'CHARGE_CODE' as const, charge_code_id: id })),
    ]
    await api.PUT('/api/v1/properties/{propertyId}/groups/{id}/routing', { params: { path: { propertyId, id: props.groupId } }, body: { instructions, excluded_lines: excluded.value } })
    notice.value = t('groupMaster.saved')
    await load()
    emit('changed')
  })

async function closeMaster(): Promise<void> {
  const m = master.value
  if (!m) return
  if (!(await confirm({ title: t('groupMaster.closeTitle', { folio: m.folio_number }), description: t('groupMaster.closeBody'), confirmLabel: t('groupMaster.close'), destructive: true }))) return
  await run(async () => {
    const propertyId = pid.value as number
    await api.POST('/api/v1/properties/{propertyId}/folios/{id}/close', { params: { path: { propertyId, id: m.id } }, body: { version: m.version } })
    notice.value = t('groupMaster.closed', { folio: m.folio_number })
    await load()
    emit('changed')
  })
}

// What is posted changed since the list was shown: nothing moved; the list is read again and the person looks at it and asks anew.
async function stale(): Promise<void> {
  moving.value = false
  notice.value = t('groupMaster.listChanged')
  noticeWarns.value = true
  await load()
}

async function moved(message: string): Promise<void> {
  moving.value = false
  notice.value = message
  noticeWarns.value = false
  await load()
}
</script>

<template>
  <Card v-if="canRead" class="mb-4" data-testid="group-master">
    <CardHeader><CardTitle>{{ t('groupMaster.title') }}</CardTitle></CardHeader>
    <CardContent>
      <p class="m-0 mb-3 text-sm text-muted-foreground">{{ t('groupMaster.hint') }}</p>
      <ErrorNotice v-if="error" :error="error" inline data-testid="master-error">
        <ul v-if="error.code === 'MASTER_FOLIO_GROUP_ACTIVE' && Array.isArray(error.context?.lines)" class="m-0 mt-1 list-disc pl-5 text-sm" data-testid="master-active-lines">
          <li v-for="(l, i) in (error.context.lines as { confirmation_number: string; status: string }[])" :key="i">{{ l.confirmation_number }} · {{ t(`status.${l.status}` as never) }}</li>
        </ul>
      </ErrorNotice>
      <p v-if="notice" :class="noticeWarns ? 'alert warning' : 'notice'" role="status" data-testid="master-notice">{{ notice }}</p>

      <div class="mb-4 flex flex-wrap items-center gap-3" data-testid="master-summary">
        <template v-if="master">
          <RouterLink :to="`/folios/${master.id}`" class="font-medium text-primary hover:underline" data-testid="master-link">{{ master.folio_number }}</RouterLink>
          <Badge :variant="master.status === 'OPEN' ? 'success' : 'outline'" data-testid="master-status">{{ master.status === 'OPEN' ? t('groupMaster.statusOpen') : t('groupMaster.statusClosed') }}</Badge>
          <span v-if="master.balance !== ''" class="text-sm tabular-nums" data-testid="master-balance">{{ t('groupMaster.balance') }} {{ $money(master.balance) }}</span>
          <span v-if="master.bill_to_company_name" class="text-sm text-muted-foreground" data-testid="master-company">{{ t('groupMaster.billedTo', { company: master.bill_to_company_name }) }}</span>
          <span v-else class="text-sm text-muted-foreground">{{ t('groupMaster.billedToContact') }}</span>
          <Button v-if="canClose && master.status === 'OPEN'" type="button" variant="outline" size="sm" class="ml-auto" :disabled="busy || owes" data-testid="master-close" @click="closeMaster">{{ t('groupMaster.close') }}</Button>
          <p v-if="canClose && master.status === 'OPEN' && owes" class="m-0 w-full text-sm text-muted-foreground" data-testid="master-close-hint">{{ t(Number(master.balance) < 0 ? 'groupMaster.closeNeedsRefund' : 'groupMaster.closeNeedsZero', { balance: $money(master.balance) }) }}</p>
        </template>
        <template v-else>
          <span class="text-sm text-muted-foreground" data-testid="master-none">{{ t('groupMaster.none') }}</span>
          <Button v-if="canManage && active" type="button" size="sm" :disabled="busy" data-testid="master-open" @click="openMaster">{{ t('groupMaster.open') }}</Button>
        </template>
      </div>

      <section data-testid="master-routing" class="grid gap-3">
        <h3 class="m-0 text-sm font-semibold">{{ t('groupMaster.routingTitle') }}</h3>
        <p class="m-0 text-sm text-muted-foreground">{{ t('groupMaster.routingHint') }}</p>
        <div class="grid gap-2">
          <label class="inline-flex items-center gap-2 text-sm"><input v-model="roomOn" type="checkbox" name="route_room" class="size-4 accent-primary" :disabled="!canManage" />{{ t('groupMaster.routeRoom') }}</label>
          <label class="inline-flex items-center gap-2 text-sm"><input v-model="allOn" type="checkbox" name="route_all" class="size-4 accent-primary" :disabled="!canManage" />{{ t('groupMaster.routeAll') }}</label>
          <div class="flex flex-wrap items-center gap-2">
            <span class="text-sm">{{ t('groupMaster.routeCodes') }}</span>
            <Badge v-for="id in picked" :key="id" variant="outline" class="gap-1" :data-testid="`route-code-${id}`">
              {{ codeOf(id)?.code ?? id }}
              <button v-if="canManage" type="button" class="cursor-pointer border-0 bg-transparent p-0 text-muted-foreground" :aria-label="t('common.remove')" @click="picked = picked.filter((x) => x !== id)">×</button>
            </Badge>
            <template v-if="canManage">
              <NativeSelect v-model.number="addCode" class="w-auto" name="route_code_add" data-testid="route-code-add" :aria-label="t('groupMaster.routeCodes')">
                <option :value="0">{{ t('groupMaster.pickCode') }}</option>
                <option v-for="c in choices" :key="c.id" :value="c.id">{{ c.code }} · {{ c.name }}</option>
              </NativeSelect>
              <Button type="button" variant="outline" size="sm" :disabled="addCode === 0" data-testid="route-code-button" @click="add">{{ t('groupMaster.addCode') }}</Button>
            </template>
          </div>
        </div>

        <div v-if="routing?.lines.length" data-testid="master-lines">
          <h4 class="m-0 mb-1 text-sm font-medium">{{ t('groupMaster.linesTitle') }}</h4>
          <p class="m-0 mb-2 text-xs text-muted-foreground">{{ t('groupMaster.linesHint') }}</p>
          <ul class="m-0 grid list-none gap-1 p-0">
            <li v-for="l in routing.lines" :key="l.reservation_room_id" class="flex flex-wrap items-center gap-3 text-sm" :data-testid="`line-${l.reservation_room_id}`">
              <label class="inline-flex items-center gap-2">
                <input type="checkbox" class="size-4 accent-primary" :checked="excluded.includes(l.reservation_room_id)" :disabled="!canManage" :name="`exclude_${l.reservation_room_id}`" @change="toggleLine(l.reservation_room_id, ($event.target as HTMLInputElement).checked)" />
                <RouterLink :to="`/reservations/${l.reservation_id}`" class="text-primary hover:underline">{{ l.confirmation_number }}</RouterLink>
              </label>
              <span class="text-muted-foreground">{{ t(`status.${l.status}` as never) }}</span>
              <Badge v-if="excluded.includes(l.reservation_room_id)" variant="outline">{{ t('groupMaster.staysOut') }}</Badge>
            </li>
          </ul>
        </div>

        <div v-if="canManage && active" class="flex justify-end">
          <Button type="button" :disabled="busy || !dirty" data-testid="routing-save" @click="save">{{ t('groupMaster.save') }}</Button>
        </div>
      </section>

      <section v-if="posted.length" class="mt-4 rounded-lg border border-border p-3" data-testid="master-posted">
        <p class="m-0 text-sm" data-testid="posted-text">{{ t('groupMaster.postedText', { n: postedTotal, folios: postedFolios }) }}</p>
        <p class="m-0 mt-1 text-xs text-muted-foreground">{{ t('groupMaster.postedHint') }}</p>
        <div class="mt-2 flex justify-end">
          <Button v-if="auth.can('folio.reverse', pid)" type="button" variant="outline" size="sm" data-testid="posted-move" @click="moving = true">{{ t('groupMaster.postedMove') }}</Button>
        </div>
      </section>
    </CardContent>
  </Card>
  <GroupMovePosted v-if="moving && master" :group-id="groupId" :master="master" :posted="posted" @done="moved" @close="moving = false" @stale="stale" />
</template>
