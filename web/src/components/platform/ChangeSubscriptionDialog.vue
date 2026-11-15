<script setup lang="ts">
import { X } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { platformApi } from '@/api/platformClient'
import { ApiError } from '@/api/problem'
import type { components } from '@/api/schema'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { DateInput } from '@/components/ui/date-input'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'

type Tenant = components['schemas']['PlatformTenantDetail']
type Plan = components['schemas']['PlatformPlan']
type Resource = 'properties' | 'rooms' | 'users'

/**
 * Change the subscription of a tenant: plan, status, paid-through date, grace days and the tenant-specific quotas (an empty quota follows the plan). Only what changed is sent
 * (PATCH `/tenants/{code}/subscription`); a quota that was set and is now empty goes in `clear_limits`. `focus="quotas"` opens it with the cursor in the quota fieldset ("Set quotas").
 */
const props = defineProps<{ open: boolean; tenant: Tenant; plans: Plan[]; focus?: 'quotas' }>()
const emit = defineEmits<{ 'update:open': [boolean]; saved: [] }>()

const RESOURCES: Resource[] = ['properties', 'rooms', 'users']
const KEY = { properties: 'max_properties', rooms: 'max_rooms', users: 'max_users' } as const

const plan = ref('')
const status = ref<'ACTIVE' | 'TRIAL' | 'CANCELLED'>('ACTIVE')
const paidThrough = ref<string | null>('')
const noEnd = ref(false)
const graceDays = ref<string | number>('')
const quota = ref<Record<Resource, string | number>>({ properties: '', rooms: '', users: '' })
const busy = ref(false)
const error = ref<ApiError | null>(null)
const invalid = ref<Record<string, string>>({})

const current = computed(() => props.tenant.subscription)
const planOptions = computed(() => props.plans.filter((p) => p.is_active || p.code === props.tenant.summary.plan_code))
const planOf = computed(() => props.plans.find((p) => p.code === plan.value))

function reset(): void {
  const s = current.value
  plan.value = props.tenant.summary.plan_code
  status.value = s.status
  paidThrough.value = s.paid_through ?? ''
  noEnd.value = s.paid_through === null
  graceDays.value = String(s.grace_days)
  quota.value = {
    properties: s.overrides.max_properties === null ? '' : String(s.overrides.max_properties),
    rooms: s.overrides.max_rooms === null ? '' : String(s.overrides.max_rooms),
    users: s.overrides.max_users === null ? '' : String(s.overrides.max_users),
  }
  error.value = null
  invalid.value = {}
}

watch(
  () => props.open,
  (now) => {
    if (now) reset()
  },
  { immediate: true },
)

// a number input gives its model back as a number once it is typed in: the fields are read as text
const txt = (v: unknown): string => String(v ?? '').trim()
const onlyDigits = /^\d+$/
const planLimit = (r: Resource): number | null => planOf.value?.limits[KEY[r]] ?? null
const planText = (r: Resource): string => {
  const n = planLimit(r)
  return n === null ? t('platform.tenant.change.unlimited') : String(n)
}
const label = (r: Resource): string => t(`platform.tenant.change.quota${r[0]!.toUpperCase()}${r.slice(1)}` as never)

/** The body of the PATCH: only what changed. */
function buildBody(): components['schemas']['PlatformChangeSubscription'] {
  const s = current.value
  const body: components['schemas']['PlatformChangeSubscription'] = {}
  if (plan.value !== props.tenant.summary.plan_code) body.plan_code = plan.value
  if (status.value !== s.status) body.status = status.value
  if (noEnd.value) {
    if (s.paid_through !== null) body.no_end = true
  } else if (paidThrough.value && paidThrough.value !== s.paid_through) body.paid_through = paidThrough.value
  if (txt(graceDays.value) !== '' && Number(graceDays.value) !== s.grace_days) body.grace_days = Number(graceDays.value)
  const limits: NonNullable<components['schemas']['PlatformChangeSubscription']['limits']> = {}
  const clear: NonNullable<components['schemas']['PlatformChangeSubscription']['clear_limits']> = []
  for (const r of RESOURCES) {
    const was = s.overrides[KEY[r]]
    const now = txt(quota.value[r])
    if (now === '') {
      if (was !== null) clear.push(r)
    } else if (Number(now) !== was) limits[KEY[r]] = Number(now)
  }
  if (Object.keys(limits).length) body.limits = limits
  if (clear.length) body.clear_limits = clear
  return body
}

const changed = computed(() => Object.keys(buildBody()).length > 0)

function validate(): boolean {
  const out: Record<string, string> = {}
  const g = txt(graceDays.value)
  if (!onlyDigits.test(g) || Number(g) > 90) out.grace_days = t('platform.tenant.change.graceInvalid')
  for (const r of RESOURCES) {
    const q = txt(quota.value[r])
    if (q !== '' && (!onlyDigits.test(q) || Number(q) < 1)) out[`quota_${r}`] = t('platform.tenant.change.quotaInvalid')
  }
  if (status.value === 'TRIAL' && (noEnd.value || !paidThrough.value)) out.paid_through = t('platform.tenant.change.trialNeedsDate')
  invalid.value = out
  return Object.keys(out).length === 0
}

async function submit(): Promise<void> {
  if (busy.value || !validate()) return
  const body = buildBody()
  if (Object.keys(body).length === 0) {
    emit('update:open', false)
    return
  }
  busy.value = true
  error.value = null
  try {
    await platformApi.PATCH('/api/v1/platform/tenants/{code}/subscription', { params: { path: { code: props.tenant.code } }, body })
    emit('update:open', false)
    emit('saved')
  } catch (e) {
    if (!(e instanceof ApiError)) throw e
    error.value = e
  } finally {
    busy.value = false
  }
}

const fieldError = (name: string, api: string): string | undefined => invalid.value[name] ?? error.value?.fieldMessage(api)
</script>

<template>
  <Dialog :open="open" @update:open="(v) => emit('update:open', v)">
    <DialogContent class="max-h-[85vh] max-w-xl overflow-y-auto p-5" data-testid="change-subscription-dialog" :aria-describedby="undefined" @interact-outside.prevent>
      <form novalidate @submit.prevent="submit">
        <div class="mb-3 flex items-start justify-between gap-3">
          <DialogTitle>{{ t('platform.tenant.change.title', { code: tenant.code }) }}</DialogTitle>
          <Button type="button" variant="ghost" size="icon" :aria-label="t('common.close')" data-testid="dialog-close" @click="emit('update:open', false)"><X /></Button>
        </div>
        <ErrorNotice v-if="error" :error="error" inline class="mb-3" />
        <div class="grid gap-3 sm:grid-cols-2">
          <FormField :label="t('platform.tenant.change.plan')" :error="fieldError('plan', 'plan_code')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="plan" name="plan_code" :data-autofocus="focus === 'quotas' ? undefined : ''">
                <option v-for="p in planOptions" :key="p.code" :value="p.code">{{ p.code }} · {{ p.name }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('platform.tenant.change.status')" :error="fieldError('status', 'status')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="status" name="status">
                <option value="ACTIVE">{{ t('platform.tenant.change.statusActive') }}</option>
                <option value="TRIAL">{{ t('platform.tenant.change.statusTrial') }}</option>
                <option v-if="current.status === 'CANCELLED'" value="CANCELLED">{{ t('platform.tenant.change.statusCancelled') }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('platform.tenant.change.paidThrough')" :error="fieldError('paid_through', 'paid_through')">
            <template #default="{ id, describedBy, invalid: bad }">
              <DateInput :id="id" v-model="paidThrough" name="paid_through" :disabled="noEnd" :aria-describedby="describedBy" :aria-invalid="bad || undefined" />
              <Checkbox v-model="noEnd" name="no_end" data-testid="no-end" label-class="mt-5 text-sm">{{ t('platform.tenant.change.noEnd') }}</Checkbox>
            </template>
          </FormField>
          <FormField :label="t('platform.tenant.change.graceDays')" :hint="t('platform.tenant.change.graceHint')" :error="fieldError('grace_days', 'grace_days')">
            <template #default="{ id, describedBy, invalid: bad }">
              <Input :id="id" v-model="graceDays" name="grace_days" type="number" min="0" max="90" inputmode="numeric" :aria-describedby="describedBy" :aria-invalid="bad || undefined" />
            </template>
          </FormField>
        </div>
        <fieldset class="mt-4 rounded-md border border-border p-3" data-testid="change-quotas">
          <legend class="px-1 text-sm font-medium">{{ t('platform.tenant.change.quotas') }}</legend>
          <div class="grid gap-3 sm:grid-cols-3">
            <FormField v-for="(r, i) in RESOURCES" :key="r" :label="label(r)" :error="fieldError(`quota_${r}`, `limit_${r}`)">
              <template #default="{ id, describedBy, invalid: bad }">
                <Input
                  :id="id"
                  :data-autofocus="focus === 'quotas' && i === 0 ? '' : undefined"
                  v-model="quota[r]"
                  :name="`limit_${r}`"
                  type="number"
                  min="1"
                  inputmode="numeric"
                  :placeholder="planLimit(r) === null ? '∞' : String(planLimit(r))"
                  :aria-describedby="describedBy"
                  :aria-invalid="bad || undefined"
                />
                <span class="text-xs text-muted-foreground" :data-testid="`quota-note-${r}`">{{ t('platform.tenant.change.quotaNote', { plan: planText(r), used: tenant.summary.usage[r] }) }}</span>
              </template>
            </FormField>
          </div>
        </fieldset>
        <p class="m-0 mt-3 rounded-md bg-muted p-3 text-sm" data-testid="change-note">{{ t('platform.tenant.change.note') }}</p>
        <div class="mt-5 flex justify-end gap-2">
          <Button type="button" variant="outline" data-testid="dialog-cancel" @click="emit('update:open', false)">{{ t('platform.tenant.change.cancel') }}</Button>
          <Button type="submit" :disabled="busy || !changed" data-testid="dialog-submit">{{ t('platform.tenant.change.save') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
