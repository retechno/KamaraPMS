<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { platformApi } from '@/api/platformClient'
import { ApiError } from '@/api/problem'
import type { components } from '@/api/schema'
import EmptyState from '@/components/app/EmptyState.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { t } from '@/i18n'
import { platformMoney } from '@/utils/platformMoney'

type Plan = components['schemas']['PlatformPlan']

/** P7: the plans with their price, limits, modules, trial and the number of tenants on each (docs/architecture/39-platform-console.md 11). */
const plans = ref<Plan[] | null>(null)
const error = ref<ApiError | null>(null)

async function load(): Promise<void> {
  error.value = null
  try {
    const { data } = await platformApi.GET('/api/v1/platform/plans')
    plans.value = data?.items ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    plans.value = []
  }
}
onMounted(load)

const OPTIONAL = ['HOUSEKEEPING', 'ACCOUNTING', 'POS', 'INVENTORY', 'BANQUET'] as const

/** Core is always there; a plan with every optional module says so in one chip. */
const moduleChips = (p: Plan): string[] =>
  OPTIONAL.every((m) => p.modules.includes(m))
    ? [t('platform.plans.allModules')]
    : [t('platform.plans.moduleShort.CORE'), ...OPTIONAL.filter((m) => p.modules.includes(m)).map((m) => t(`platform.plans.moduleShort.${m}`))]

const price = (p: Plan): string => platformMoney(p.currency_code, p.price)
const period = (p: Plan): string => (p.billing_period_months === 1 ? t('platform.plans.perMonth') : t('platform.plans.perMonths', { n: p.billing_period_months }))
const limit = (v: number | null): string => (v === null ? t('platform.plans.unlimited') : String(v))
const trial = (p: Plan): string => (p.code === 'LEGACY' ? t('platform.plans.legacyTrial') : p.trial_days > 0 ? t('platform.plans.trialDays', { n: p.trial_days }) : t('platform.plans.noTrial'))
const tenants = (p: Plan): string => t(p.tenant_count === 1 ? 'platform.plans.tenantsOne' : 'platform.plans.tenantsMany', { n: p.tenant_count })
const limits = (p: Plan) => [
  { key: 'properties', label: t('platform.plans.properties'), value: limit(p.limits.max_properties) },
  { key: 'rooms', label: t('platform.plans.rooms'), value: limit(p.limits.max_rooms) },
  { key: 'users', label: t('platform.plans.users'), value: limit(p.limits.max_users) },
]
const loading = computed(() => plans.value === null)
</script>

<template>
  <PageHeader :title="t('platform.plans.title')" :description="t('platform.plans.description')">
    <template #actions>
      <Button as-child><RouterLink to="/platform/plans/new" data-testid="new-plan">{{ t('platform.plans.newPlan') }}</RouterLink></Button>
    </template>
  </PageHeader>

  <ErrorNotice v-if="error" :error="error" inline />
  <div v-else-if="loading" class="grid gap-4 [grid-template-columns:repeat(auto-fill,minmax(min(340px,100%),1fr))]" data-testid="plans-loading">
    <Skeleton v-for="n in 3" :key="n" class="h-56 w-full" />
  </div>
  <EmptyState v-else-if="plans && plans.length === 0" :title="t('platform.plans.emptyTitle')" :description="t('platform.plans.emptyHint')" :action-label="t('platform.plans.newPlan')" action-to="/platform/plans/new" />
  <ul v-else class="m-0 grid list-none gap-4 p-0 [grid-template-columns:repeat(auto-fill,minmax(min(340px,100%),1fr))]" data-testid="plans-list">
    <li v-for="p in plans" :key="p.code" :data-testid="`plan-${p.code}`">
      <Card :class="['grid h-full content-start gap-3 p-4', p.is_default ? 'border-primary' : '', p.is_active ? '' : 'opacity-85']">
        <div class="flex flex-wrap items-start justify-between gap-2">
          <div class="min-w-0">
            <span class="text-xs font-semibold tracking-wide text-muted-foreground">{{ p.code }}</span>
            <h2 class="m-0 text-lg font-semibold">{{ p.name }}</h2>
          </div>
          <div class="flex flex-wrap gap-1.5">
            <Badge v-if="p.is_default" variant="success" data-testid="badge-default">{{ t('platform.plans.isDefault') }}</Badge>
            <Badge v-if="!p.is_active" variant="outline" data-testid="badge-not-for-sale">{{ t('platform.plans.notForSale') }}</Badge>
          </div>
        </div>
        <div class="flex items-baseline gap-1.5">
          <span class="text-xl font-semibold" data-testid="plan-price">{{ price(p) }}</span>
          <span class="text-sm text-muted-foreground">{{ period(p) }}</span>
        </div>
        <dl class="m-0 grid grid-cols-3 gap-2">
          <div v-for="l in limits(p)" :key="l.key" class="rounded-md bg-muted px-2 py-1.5" :data-testid="`limit-${l.key}`">
            <dt class="text-xs text-muted-foreground">{{ l.label }}</dt>
            <dd class="m-0 text-sm font-semibold">{{ l.value }}</dd>
          </div>
        </dl>
        <ul class="m-0 flex list-none flex-wrap gap-1.5 p-0" :aria-label="t('platform.plans.modules')">
          <li v-for="m in moduleChips(p)" :key="m"><Badge variant="secondary">{{ m }}</Badge></li>
        </ul>
        <div class="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm">
          <span class="text-muted-foreground" data-testid="plan-trial">{{ trial(p) }}</span>
          <span aria-hidden="true" class="text-muted-foreground">·</span>
          <RouterLink :to="{ path: '/platform/tenants', query: { plan: p.code } }" class="text-primary underline-offset-2 hover:underline pointer-coarse:inline-flex pointer-coarse:min-h-[44px] pointer-coarse:items-center" data-testid="plan-tenants">{{ tenants(p) }}</RouterLink>
          <RouterLink :to="`/platform/plans/${encodeURIComponent(p.code)}`" class="ml-auto text-primary underline-offset-2 hover:underline pointer-coarse:inline-flex pointer-coarse:min-h-[44px] pointer-coarse:items-center" data-testid="plan-edit">{{ t('platform.plans.edit') }}</RouterLink>
        </div>
      </Card>
    </li>
  </ul>
</template>
