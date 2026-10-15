<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { FolioOutletLock, PosOutlet } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { Button } from '@/components/ui/button'
import OutletLockForm, { type OutletLockDraft } from '@/components/folio/OutletLockForm.vue'
import { Info, Lock, LockOpen } from 'lucide-vue-next'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatDate, formatMoney } from '@/utils/format'

/**
 * The lock of a folio for outlet charges on the folio screen (docs/architecture/34-pos.md section 6, mockup 13): closed until the front desk opens it with a limit, a validity and the outlets
 * that may charge it; it closes again by itself at check-out and when the limit is reached. The server decides; this only asks.
 */
const props = defineProps<{ folioId: number; untilLabel?: string }>()
const emit = defineEmits<{ changed: [lock: FolioOutletLock] }>()

const auth = useAuthStore()
const property = usePropertyStore()
const lock = ref<FolioOutletLock | null>(null)
const outlets = ref<PosOutlet[]>([])
const draft = ref<OutletLockDraft>({ limit: '', mode: 'UNTIL_CLOSE', outletIds: [] })
const editing = ref(false)
const busy = ref(false)
const error = ref<ApiError | null>(null)
const loadError = ref<ApiError | null>(null)

const pid = computed(() => property.currentId)
const canOpen = computed(() => auth.can('folio.outlet_charge_open', pid.value))
const decimals = computed(() => property.current?.currency_decimals ?? 0)
const isOpen = computed(() => lock.value?.status === 'OPEN')
const untilText = computed(() =>
  props.untilLabel ?? (lock.value?.checkout_date ? t('outletLock.untilCheckoutOn', { date: formatDate(lock.value.checkout_date) }) : t('outletLock.untilCheckout')),
)
const showForm = computed(() => editing.value || (!isOpen.value && canOpen.value && !!lock.value?.can_open))
const reasonKey = computed(() => `outletLock.reason_${lock.value?.reason ?? 'NEVER_OPENED'}`)
const whyNotKey = computed(() => `outletLock.whyNot_${lock.value?.why_not ?? ''}`)
const limitMissing = computed(() => draft.value.limit.trim() === '' || Number(draft.value.limit) <= 0)
const invalid = computed(() => limitMissing.value || draft.value.outletIds.length === 0)

function adopt(l: FolioOutletLock): void {
  lock.value = l
  draft.value = {
    limit: l.limit_amount ? stripZeros(l.limit_amount) : '',
    mode: (l.valid_mode as OutletLockDraft['mode'] | null) ?? 'UNTIL_CLOSE',
    outletIds: l.outlets.filter((o) => o.is_active).map((o) => o.id),
  }
}
const stripZeros = (v: string): string => (v.includes('.') ? v.replace(/\.?0+$/, '') : v)
const shown = (v: string | null | undefined): string => formatMoney(stripZeros(v ?? '0'))

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  loadError.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/folios/{id}/outlet-lock', { params: { path: { propertyId, id: props.folioId } } })
    if (data && typeof data.status === 'string') adopt({ ...data, outlets: data.outlets ?? [] })
  } catch (e) {
    loadError.value = e instanceof ApiError ? e : null
  }
}

async function loadOutlets(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !canOpen.value) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/pos/outlets', { params: { path: { propertyId }, query: { active: true } } })
    outlets.value = data?.data ?? []
  } catch {
    outlets.value = []
  }
}

async function save(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || invalid.value) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.PUT('/api/v1/properties/{propertyId}/folios/{id}/outlet-lock', {
      params: { path: { propertyId, id: props.folioId } },
      body: { limit_amount: draft.value.limit.trim(), valid_mode: draft.value.mode, outlet_ids: draft.value.outletIds, version: isOpen.value ? lock.value?.version : undefined },
    })
    if (data) {
      adopt(data)
      editing.value = false
      emit('changed', data)
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function lockNow(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/folios/{id}/outlet-lock/lock', {
      params: { path: { propertyId, id: props.folioId } },
      body: { version: lock.value?.version },
    })
    if (data) {
      adopt(data)
      editing.value = false
      emit('changed', data)
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

onMounted(() => {
  void load()
  void loadOutlets()
})
watch(() => props.folioId, () => {
  editing.value = false
  void load()
})
</script>

<template>
  <section class="rounded-xl border border-border bg-card p-4" data-testid="outlet-lock-card" :data-status="lock?.status">
    <ErrorNotice v-if="loadError" :error="loadError" inline data-testid="lock-load-error" />
    <template v-else-if="lock">
      <header class="flex items-start gap-3">
        <span class="grid size-11 shrink-0 place-items-center rounded-xl" :class="isOpen ? 'bg-success/15 text-success-text' : 'bg-warning/20 text-foreground'" aria-hidden="true">
          <LockOpen v-if="isOpen" class="size-5" /><Lock v-else class="size-5" />
        </span>
        <div class="min-w-0">
          <h3 class="m-0 text-base font-semibold">
            {{ t('outletLock.title') }}: <span data-testid="lock-status">{{ isOpen ? t('outletLock.open') : t('outletLock.locked') }}</span>
          </h3>
          <p class="m-0 text-sm text-muted-foreground" data-testid="lock-locked-note">{{ isOpen ? t('outletLock.openHint') : t('outletLock.lockedHint') }}{{ ' ' }}<template v-if="!isOpen">{{ t(reasonKey) }}</template></p>
        </div>
      </header>

      <p v-if="!isOpen && !lock.can_open" class="m-0 mt-3 text-sm text-muted-foreground" data-testid="lock-cannot-open">{{ t(whyNotKey) }}</p>

      <!-- open: what is used -->
      <div v-if="isOpen && !editing" class="mt-3 flex flex-col gap-3" data-testid="lock-open-summary">
        <div class="grid grid-cols-3 gap-2 text-sm">
          <div class="rounded-lg border border-border px-3 py-2">
            <span class="block text-xs text-muted-foreground">{{ t('outletLock.limit') }}</span>
            <strong class="tabular-nums" data-testid="lock-limit-shown">{{ shown(lock.limit_amount) }}</strong>
          </div>
          <div class="rounded-lg border border-border px-3 py-2">
            <span class="block text-xs text-muted-foreground">{{ t('outletLock.used') }}</span>
            <strong class="tabular-nums" data-testid="lock-used">{{ shown(lock.used) }}</strong>
          </div>
          <div class="rounded-lg border border-border px-3 py-2">
            <span class="block text-xs text-muted-foreground">{{ t('outletLock.remaining') }}</span>
            <strong class="tabular-nums" data-testid="lock-remaining">{{ shown(lock.remaining) }}</strong>
          </div>
        </div>
        <p class="m-0 text-sm text-muted-foreground">
          {{ lock.valid_mode === 'TODAY' ? t('outletLock.validTodayOn', { date: formatDate(lock.valid_on ?? lock.business_date) }) : t('outletLock.validUntilClose', { until: untilText }) }}
          · {{ lock.outlets.map((o) => o.name).join(', ') }}
        </p>
        <div v-if="canOpen" class="flex flex-wrap gap-2">
          <Button variant="outline" data-testid="lock-change-button" @click="editing = true">{{ t('outletLock.change') }}</Button>
          <Button variant="outline" :disabled="busy" data-testid="lock-lock-button" @click="lockNow">{{ t('outletLock.lockNow') }}</Button>
        </div>
      </div>

      <!-- the form: a locked folio shows it at once, as the design of the front desk does -->
      <form v-else-if="showForm" class="mt-4 flex flex-col gap-4" novalidate @submit.prevent="save">
        <ErrorNotice v-if="error" :error="error" inline data-testid="lock-error" />
        <OutletLockForm v-model="draft" :outlets="outlets" :decimals="decimals" :until-label="untilText" :disabled="busy" :id-prefix="`folio-${folioId}`" />
        <p class="m-0 flex items-start gap-2 rounded-lg bg-muted/50 px-3 py-2 text-xs text-muted-foreground"><Info class="mt-0.5 size-4 shrink-0" aria-hidden="true" />{{ t('outletLock.autoLockHint') }}</p>
        <p v-if="invalid" class="m-0 text-sm text-muted-foreground" data-testid="lock-missing">
          {{ limitMissing ? t('outletLock.missingLimit') : t('outletLock.missingOutlet') }}
        </p>
        <div class="flex justify-end gap-2">
          <Button v-if="editing" type="button" variant="outline" data-testid="lock-cancel" @click="editing = false; adopt(lock!)">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy || invalid" data-testid="lock-save">{{ isOpen ? t('common.save') : t('outletLock.openFor') }}</Button>
        </div>
      </form>
    </template>
  </section>
</template>
