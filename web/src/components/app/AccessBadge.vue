<script setup lang="ts">
import { computed } from 'vue'
import { Badge } from '@/components/ui/badge'
import { t } from '@/i18n'

/** The state of a subscription in one word: a trial (while it runs), active, in its grace period, or read only. The word is always there, the colour only adds to it. */
const props = defineProps<{ access: 'FULL' | 'GRACE' | 'READ_ONLY'; status: string }>()

const label = computed(() => (props.access === 'FULL' ? (props.status === 'TRIAL' ? t('subscription.state.TRIAL') : t('subscription.state.FULL')) : t(`subscription.state.${props.access}` as never)))
const variant = computed(() => (props.access === 'READ_ONLY' ? ('destructive' as const) : props.access === 'GRACE' ? ('warning' as const) : props.status === 'TRIAL' ? ('secondary' as const) : ('success' as const)))
</script>

<template>
  <Badge :variant="variant" data-testid="access-badge">{{ label }}</Badge>
</template>
