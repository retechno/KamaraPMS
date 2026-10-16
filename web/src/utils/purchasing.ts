import { t } from '@/i18n'

/** The variant of the Badge for the state of a purchasing document (docs/architecture/35-inventory.md 5.3). */
export type StatusVariant = 'default' | 'secondary' | 'outline' | 'success' | 'warning' | 'destructive'

export function requestVariant(status: string): StatusVariant {
  switch (status) {
    case 'SUBMITTED':
      return 'warning'
    case 'APPROVED':
    case 'CONVERTED':
      return 'success'
    case 'RETURNED':
      return 'destructive'
    case 'CANCELLED':
      return 'outline'
    default:
      return 'secondary'
  }
}

export function orderVariant(status: string): StatusVariant {
  switch (status) {
    case 'ORDERED':
      return 'warning'
    case 'PARTIAL':
      return 'warning'
    case 'RECEIVED':
    case 'CLOSED':
      return 'success'
    case 'CANCELLED':
      return 'outline'
    default:
      return 'secondary'
  }
}

export function draftVariant(status: string | null | undefined): StatusVariant {
  switch (status) {
    case 'OPEN':
      return 'warning'
    case 'PARTIAL':
      return 'warning'
    case 'BILLED':
      return 'success'
    default:
      return 'outline'
  }
}

/** "2 of 3 · Cost control" for the stage of a request in its chain; an approved request says it is done. */
export function stageText(stage: { done: number; total: number; name: string } | null | undefined, status: string): string {
  if (status === 'APPROVED' || status === 'CONVERTED') return stage ? t('inventory.request.stageDone', { total: stage.total }) : t('inventory.request.stageNone')
  if (status === 'DRAFT') return t('inventory.request.stageNotSubmitted')
  if (status === 'CANCELLED') return ''
  if (!stage) return ''
  const position = status === 'RETURNED' ? stage.done + 1 : Math.min(stage.done + 1, stage.total)
  const base = t('inventory.request.stage', { n: position, total: stage.total })
  return stage.name ? `${base} · ${stage.name}` : base
}

/** The quantity of a purchase unit as the page writes it: the zeros the server keeps are dropped. */
export function trimDecimals(value: string): string {
  return value.includes('.') ? value.replace(/\.?0+$/, '') : value
}
