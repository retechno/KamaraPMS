<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { platformApi } from '@/api/platformClient'
import { ApiError } from '@/api/problem'
import type { components } from '@/api/schema'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import MoneyInput from '@/components/app/MoneyInput.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import { vAutofocus } from '@/directives/autofocus'
import { t } from '@/i18n'

type Plan = components['schemas']['PlatformPlan']
type PlanInput = components['schemas']['PlatformPlanInput']
type Affected = components['schemas']['PlatformAffected']
type Module = Plan['modules'][number]

/**
 * P8: a plan, new (`/platform/plans/new`) or edited (`/platform/plans/:code`). The plan is read from the list (there is no read of a single plan); the other plans tell which one is the
 * default now. After a change the tenants that are over a limit of the plan are listed, as a warning: the change is already made (docs/architecture/39-platform-console.md 11).
 */
const props = defineProps<{ code?: string }>()
const router = useRouter()
const isEdit = computed(() => !!props.code)

const OPTIONAL: readonly Module[] = ['HOUSEKEEPING', 'ACCOUNTING', 'POS', 'INVENTORY', 'BANQUET']
const PERIODS = [1, 3, 6, 12] as const
const QUOTAS = ['max_properties', 'max_rooms', 'max_users'] as const
type Quota = (typeof QUOTAS)[number]

const plans = ref<Plan[] | null>(null)
const loadError = ref<ApiError | null>(null)
const notFound = ref(false)
const original = ref<Plan | null>(null)

const form = reactive({
  code: '',
  name: '',
  price: '',
  currency: 'IDR',
  period: 1 as number,
  modules: [] as Module[],
  isActive: true,
  isDefault: false,
})
// A quota or the trial: the number as typed, and the box that says there is none. A box that is ticked empties the field.
const quotas = reactive({
  max_properties: { value: '', none: true },
  max_rooms: { value: '', none: true },
  max_users: { value: '', none: true },
  trial: { value: '', none: true },
})
const QUOTA_KEYS = [...QUOTAS, 'trial'] as const
for (const k of QUOTA_KEYS) {
  watch(() => quotas[k].none, (none) => {
    if (none) quotas[k].value = ''
  })
}

const busy = ref(false)
const error = ref<ApiError | null>(null)
const saved = ref(false)
const affected = ref<Affected[]>([])
const localErrors = reactive<Record<string, string>>({})

const fieldError = (field: string): string | undefined => localErrors[field] ?? error.value?.fieldMessage(field)

function fill(p: Plan): void {
  original.value = p
  form.code = p.code
  form.name = p.name
  form.price = p.price.replace(/\.0+$/, '')
  form.currency = p.currency_code
  form.period = p.billing_period_months
  form.modules = [...p.modules]
  form.isActive = p.is_active
  form.isDefault = p.is_default
  quotas.max_properties = { value: p.limits.max_properties === null ? '' : String(p.limits.max_properties), none: p.limits.max_properties === null }
  quotas.max_rooms = { value: p.limits.max_rooms === null ? '' : String(p.limits.max_rooms), none: p.limits.max_rooms === null }
  quotas.max_users = { value: p.limits.max_users === null ? '' : String(p.limits.max_users), none: p.limits.max_users === null }
  quotas.trial = { value: p.trial_days > 0 ? String(p.trial_days) : '', none: p.trial_days === 0 }
}

async function load(): Promise<void> {
  try {
    const { data } = await platformApi.GET('/api/v1/platform/plans')
    plans.value = data?.items ?? []
    if (props.code) {
      const p = plans.value.find((x) => x.code.toUpperCase() === props.code?.toUpperCase())
      if (p) fill(p)
      else notFound.value = true
    }
  } catch (e) {
    loadError.value = e instanceof ApiError ? e : null
    plans.value = []
  }
}
onMounted(load)

/** The plan that is the default now, other than the one being edited. */
const currentDefault = computed(() => plans.value?.find((p) => p.is_default) ?? null)
const defaultIsSelf = computed(() => isEdit.value && !!original.value?.is_default)
const defaultHint = computed(() =>
  defaultIsSelf.value ? t('platform.plans.form.defaultSelf') : currentDefault.value ? t('platform.plans.form.defaultNow', { code: currentDefault.value.code }) : t('platform.plans.form.defaultNone'),
)
const tenantCount = computed(() => original.value?.tenant_count ?? 0)

function build(): PlanInput | null {
  for (const k of Object.keys(localErrors)) delete localErrors[k]
  const limits: PlanInput['limits'] = {}
  for (const k of QUOTAS) {
    if (quotas[k].none) continue
    const text = String(quotas[k].value ?? '').trim() // a number input hands over a number
    const n = Number(text)
    if (!text || !Number.isInteger(n)) localErrors[k] = t('platform.plans.form.numberOrUnlimited')
    else limits[k] = n
  }
  let trial = 0
  if (!quotas.trial.none) {
    const text = String(quotas.trial.value ?? '').trim()
    trial = Number(text)
    if (!text || !Number.isInteger(trial)) localErrors.trial_days = t('platform.plans.form.numberOrUnlimited')
  }
  if (Object.keys(localErrors).length > 0) return null
  return {
    ...(isEdit.value ? {} : { code: form.code.trim() }),
    name: form.name.trim(),
    modules: [...form.modules],
    limits,
    price: form.price || '0',
    currency_code: form.currency,
    billing_period_months: form.period as 1 | 3 | 6 | 12,
    trial_days: trial,
    is_active: form.isActive,
    is_default: form.isDefault,
  }
}

async function save(): Promise<void> {
  saved.value = false
  affected.value = []
  error.value = null
  const body = build()
  if (!body) return
  busy.value = true
  try {
    if (isEdit.value && props.code) {
      const { data } = await platformApi.PATCH('/api/v1/platform/plans/{code}', { params: { path: { code: props.code } }, body })
      if (data) {
        fill(data.plan)
        plans.value = (plans.value ?? []).map((p) => (p.code === data.plan.code ? data.plan : data.plan.is_default ? { ...p, is_default: false } : p))
        affected.value = data.affected
      }
      saved.value = true
    } else {
      await platformApi.POST('/api/v1/platform/plans', { body })
      await router.push('/platform/plans')
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

const periodLabel = (n: number): string => (n === 1 ? t('platform.plans.form.month') : t('platform.plans.form.months', { n }))
const moduleName = (m: string): string => t(`platform.plans.module.${m}` as never)
const moduleHint = (m: string): string => t(`platform.plans.moduleHint.${m}` as never)
const quotaLabel = (k: Quota | 'trial'): string => t(`platform.plans.form.${k === 'trial' ? 'trialDays' : k === 'max_properties' ? 'maxProperties' : k === 'max_rooms' ? 'maxRooms' : 'maxUsers'}` as never)
const quotaNone = (k: Quota | 'trial'): string => (k === 'trial' ? t('platform.plans.form.noTrial') : t('platform.plans.form.unlimited'))
const quotaField = (k: Quota | 'trial'): string => (k === 'trial' ? 'trial_days' : k)
</script>

<template>
  <nav v-if="isEdit" :aria-label="t('platform.plans.form.breadcrumb')" class="mb-2 text-sm text-muted-foreground">
    <RouterLink to="/platform/plans" class="text-primary underline-offset-2 hover:underline">{{ t('platform.plans.title') }}</RouterLink> <span aria-hidden="true">/</span> {{ code }}
  </nav>
  <PageHeader
    :title="isEdit ? t('platform.plans.form.titleEdit', { code: code ?? '' }) : t('platform.plans.form.titleNew')"
    :description="isEdit ? (original ? t('platform.plans.form.descriptionEdit', { n: tenantCount }) : undefined) : t('platform.plans.form.descriptionNew')"
  />

  <ErrorNotice v-if="loadError" :error="loadError" inline />
  <div v-else-if="plans === null" class="grid gap-4" data-testid="plan-loading">
    <Skeleton class="h-40 w-full" />
    <Skeleton class="h-40 w-full" />
  </div>
  <div v-else-if="notFound" class="alert" role="alert" data-testid="plan-not-found">
    <p class="m-0">{{ t('platform.plans.form.notFound') }}</p>
    <p class="m-0 mt-2"><RouterLink to="/platform/plans" class="text-primary underline-offset-2 hover:underline">{{ t('platform.plans.form.back') }}</RouterLink></p>
  </div>

  <form v-else v-autofocus class="grid gap-4" novalidate data-testid="plan-form" @submit.prevent="save">
    <div v-if="saved" class="notice" role="status" data-testid="plan-saved">{{ t('platform.plans.form.saved') }}</div>
    <div v-if="affected.length" class="alert warning" role="status" data-testid="plan-affected">
      <p class="m-0 font-medium">{{ t('platform.plans.form.affectedTitle') }}</p>
      <ul class="m-0 mt-1 list-disc pl-5">
        <li v-for="a in affected" :key="`${a.tenant_code}-${a.resource}`">
          <RouterLink :to="`/platform/tenants/${encodeURIComponent(a.tenant_code)}`" class="font-medium text-primary underline-offset-2 hover:underline">{{ a.tenant_code }}</RouterLink>:
          {{ t('platform.plans.form.affectedRow', { used: a.used, resource: t(`platform.plans.form.resource.${a.resource}` as never), limit: a.limit }) }}
        </li>
      </ul>
      <p class="m-0 mt-1 text-sm">{{ t('platform.plans.form.affectedNote') }}</p>
    </div>
    <ErrorNotice :error="error" />

    <Card aria-labelledby="pf-identity">
      <CardHeader><CardTitle id="pf-identity">{{ t('platform.plans.form.identity') }}</CardTitle></CardHeader>
      <CardContent class="grid gap-4 sm:grid-cols-2">
        <FormField :label="t('platform.plans.form.code')" :hint="isEdit ? t('platform.plans.form.codeLocked') : t('platform.plans.form.codeHint')" :error="fieldError('code')" :required="!isEdit">
          <template #default="{ id, describedBy, invalid }">
            <Input :id="id" name="code" :model-value="form.code" :readonly="isEdit" maxlength="30" autocapitalize="characters" :aria-describedby="describedBy" :aria-invalid="invalid || undefined" @update:model-value="form.code = String($event ?? '').toUpperCase()" />
          </template>
        </FormField>
        <FormField :label="t('platform.plans.form.name')" :error="fieldError('name')" required>
          <template #default="{ id, describedBy, invalid }">
            <Input :id="id" v-model="form.name" name="name" :data-autofocus="isEdit ? '' : undefined" maxlength="200" :aria-describedby="describedBy" :aria-invalid="invalid || undefined" />
          </template>
        </FormField>
        <FormField :label="t('platform.plans.form.price')" :error="fieldError('price')">
          <template #default="{ id, describedBy, invalid }">
            <div class="flex items-center gap-2">
              <span class="text-sm text-muted-foreground" data-testid="plan-currency">{{ form.currency }}</span>
              <MoneyInput :id="id" v-model="form.price" name="price" :aria-describedby="describedBy" :aria-invalid="invalid || undefined" />
            </div>
          </template>
        </FormField>
        <FormField :label="t('platform.plans.form.period')" :error="fieldError('billing_period_months')">
          <template #default="{ id, describedBy, invalid }">
            <NativeSelect :id="id" v-model.number="form.period" name="billing_period_months" :aria-describedby="describedBy" :aria-invalid="invalid || undefined">
              <option v-for="n in PERIODS" :key="n" :value="n">{{ periodLabel(n) }}</option>
            </NativeSelect>
          </template>
        </FormField>
      </CardContent>
    </Card>

    <Card aria-labelledby="pf-modules">
      <CardHeader><CardTitle id="pf-modules">{{ t('platform.plans.form.modules') }}</CardTitle></CardHeader>
      <CardContent>
        <ul class="m-0 grid list-none gap-2 p-0 sm:grid-cols-2" data-testid="plan-modules">
          <li class="rounded-md border border-border bg-muted px-3 py-2">
            <Checkbox :model-value="true" disabled name="module_core" data-testid="module-CORE" label-class="items-start">
              <span><span class="block text-sm font-medium">{{ moduleName('CORE') }}</span><span class="block text-sm text-muted-foreground">{{ moduleHint('CORE') }}</span></span>
            </Checkbox>
          </li>
          <li v-for="m in OPTIONAL" :key="m" class="rounded-md border border-border px-3 py-2">
            <Checkbox v-model="form.modules" :value="m" name="modules" :data-testid="`module-${m}`" label-class="items-start">
              <span><span class="block text-sm font-medium">{{ moduleName(m) }}</span><span class="block text-sm text-muted-foreground">{{ moduleHint(m) }}</span></span>
            </Checkbox>
          </li>
        </ul>
        <p v-if="fieldError('modules')" class="m-0 mt-2 text-xs text-destructive" role="alert">{{ fieldError('modules') }}</p>
      </CardContent>
    </Card>

    <Card aria-labelledby="pf-quotas">
      <CardHeader><CardTitle id="pf-quotas">{{ t('platform.plans.form.quotas') }}</CardTitle></CardHeader>
      <CardContent class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <div v-for="k in QUOTA_KEYS" :key="k" class="grid content-start gap-2">
          <FormField :label="quotaLabel(k)" :error="fieldError(quotaField(k))">
            <template #default="{ id, describedBy, invalid }">
              <Input :id="id" v-model="quotas[k].value" :name="quotaField(k)" type="number" min="1" inputmode="numeric" :disabled="quotas[k].none" :aria-describedby="describedBy" :aria-invalid="invalid || undefined" />
            </template>
          </FormField>
          <Checkbox v-model="quotas[k].none" :name="`${quotaField(k)}_none`" :data-testid="`none-${quotaField(k)}`">{{ quotaNone(k) }}</Checkbox>
        </div>
      </CardContent>
    </Card>

    <Card aria-labelledby="pf-sale">
      <CardHeader><CardTitle id="pf-sale">{{ t('platform.plans.form.sale') }}</CardTitle></CardHeader>
      <CardContent class="grid gap-3">
        <Checkbox v-model="form.isActive" name="is_active" data-testid="is-active" label-class="items-start">
          <span><span class="block text-sm font-medium">{{ t('platform.plans.form.forSale') }}</span><span class="block text-sm text-muted-foreground">{{ t('platform.plans.form.forSaleHint') }}</span></span>
        </Checkbox>
        <Checkbox v-model="form.isDefault" name="is_default" :disabled="defaultIsSelf" data-testid="is-default" label-class="items-start">
          <span><span class="block text-sm font-medium">{{ t('platform.plans.form.makeDefault') }}</span><span class="block text-sm text-muted-foreground" data-testid="default-hint">{{ defaultHint }}</span></span>
        </Checkbox>
        <p v-if="fieldError('is_default')" class="m-0 text-xs text-destructive" role="alert">{{ fieldError('is_default') }}</p>
      </CardContent>
    </Card>

    <div class="flex flex-wrap items-center justify-end gap-2">
      <Button as-child variant="outline"><RouterLink to="/platform/plans" data-testid="cancel">{{ saved ? t('platform.plans.form.back') : t('platform.plans.form.cancel') }}</RouterLink></Button>
      <Button type="submit" :disabled="busy" data-testid="save">{{ busy ? t('platform.plans.form.saving') : t('platform.plans.form.save') }}</Button>
    </div>
  </form>
</template>
