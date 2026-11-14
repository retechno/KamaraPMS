<script setup lang="ts">
import { Lock } from 'lucide-vue-next'
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { useSubscriptionStore } from '@/stores/subscription'
import { formatBusinessDate } from '@/utils/dates'

/**
 * What the person is told about the paid period (docs/architecture/39-platform-console.md §10.3): a trial that ends within a week (everyone, and it can be put away for the rest of the day),
 * the grace period (administrators are told to renew, the others to ask them) and read only (everyone, it stays). The days and the dates are the server's, from the subscription summary.
 * The buttons of the pages stay on in read only: the server refuses and the toast says why.
 */
const subscription = useSubscriptionStore()
const auth = useAuthStore()

const s = computed(() => subscription.summary)
const kind = computed<'trial' | 'grace' | 'readOnly' | null>(() => {
  if (!s.value) return null
  if (s.value.access === 'READ_ONLY') return 'readOnly'
  if (s.value.access === 'GRACE') return 'grace'
  return s.value.is_trial_ending ? 'trial' : null
})

// "Not today": the key holds the day the trial warning was put away, so it comes back tomorrow (the business date of the server, not the browser's clock).
const dismissKey = computed(() => `kamarapms.subBanner.${auth.me?.tenant.code ?? ''}.${s.value?.paid_through ?? ''}.${s.value?.days_left ?? ''}`)
const dismissed = ref(false)
function stored(): boolean {
  try {
    return localStorage.getItem(dismissKey.value) === '1'
  } catch {
    return false
  }
}
const hidden = computed(() => kind.value === 'trial' && (dismissed.value || stored()))
function dismiss(): void {
  dismissed.value = true
  try {
    localStorage.setItem(dismissKey.value, '1')
  } catch {
    // the choice then lasts until the page is reloaded
  }
}

const date = (d: string | null | undefined): string => (d ? formatBusinessDate(d) : '')
</script>

<template>
  <div v-if="kind && !hidden" data-testid="subscription-banner" :data-kind="kind">
    <p v-if="kind === 'trial'" class="alert info flex flex-wrap items-center gap-x-3 gap-y-1" role="status">
      <span>{{ t('subscription.banner.trial', { date: date(s?.paid_through), n: s?.days_left ?? 0 }) }}</span>
      <RouterLink v-if="auth.isAdmin" to="/setup/subscription" class="font-medium text-primary hover:underline">{{ t('subscription.banner.view') }}</RouterLink>
      <Button variant="ghost" size="sm" class="ml-auto" data-testid="subscription-banner-dismiss" @click="dismiss">{{ t('subscription.banner.dismiss') }}</Button>
    </p>
    <p v-else-if="kind === 'grace'" class="alert warning" role="status">
      <template v-if="auth.isAdmin">
        {{ t('subscription.banner.graceAdmin', { paid: date(s?.paid_through), n: s?.days_left ?? 0, grace: date(s?.grace_ends_on) }) }}
        <RouterLink to="/setup/subscription" class="ml-2 font-medium text-primary hover:underline">{{ t('subscription.banner.view') }}</RouterLink>
      </template>
      <template v-else>{{ t('subscription.banner.graceOther', { n: s?.days_left ?? 0 }) }}</template>
    </p>
    <p v-else class="alert flex items-start gap-2" role="alert">
      <Lock class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
      <span>
        {{ t('subscription.banner.readOnly') }}
        <RouterLink v-if="auth.isAdmin" to="/setup/subscription" class="ml-2 font-medium underline">{{ t('subscription.banner.view') }}</RouterLink>
      </span>
    </p>
  </div>
</template>
