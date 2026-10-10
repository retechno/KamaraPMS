<script setup lang="ts">
import { FileQuestion } from 'lucide-vue-next'
import EmptyState from '@/components/app/EmptyState.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Card } from '@/components/ui/card'
import { t } from '@/i18n'
</script>

<template>
  <PageHeader :title="t('notFound.title')" />
  <Card>
    <EmptyState :icon="FileQuestion" class="py-16! [&_svg]:size-14" :title="t('notFound.heading')" :description="t('notFound.text')" :action-label="t('notFound.back')" action-to="/" data-testid="not-found" />
  </Card>
</template>
