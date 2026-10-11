<script setup lang="ts">
import { RouterLink } from 'vue-router'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { t } from '@/i18n'

/**
 * A column of the Today page: its title, how many are left, a link to the page with the whole list, and the rows (the slot). The part may not have loaded yet, or may have failed:
 * it says so inside the card and leaves the rest of the page alone.
 */
defineProps<{ title: string; count?: string; to?: string; loaded: boolean; failed: boolean; testId: string }>()
</script>

<template>
  <Card :data-testid="testId">
    <CardHeader class="flex-row items-center justify-between gap-2">
      <CardTitle class="flex flex-wrap items-baseline gap-x-2">
        {{ title }}
        <small v-if="count" class="font-normal text-muted-foreground" :data-testid="`${testId}-count`">· {{ count }}</small>
      </CardTitle>
      <Button v-if="to" as-child variant="ghost" size="sm"><RouterLink :to="to" :data-testid="`${testId}-all`">{{ t('today.all') }}</RouterLink></Button>
    </CardHeader>
    <CardContent>
      <p v-if="failed" class="m-0 mb-3 rounded-md border border-border bg-muted px-3 py-2 text-sm text-warning-text" role="status" :data-testid="`${testId}-failed`">{{ t('today.couldNotLoad') }}</p>
      <p v-if="!loaded && !failed" class="m-0 text-sm text-muted-foreground" :data-testid="`${testId}-loading`">{{ t('today.loading') }}</p>
      <slot v-else-if="loaded" />
    </CardContent>
  </Card>
</template>
