import type { VariantProps } from 'class-variance-authority'
import { cva } from 'class-variance-authority'
import { statusFill } from './statusFill'

export { default as Badge } from './Badge.vue'

export const badgeVariants = cva(
  'inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-xs font-medium whitespace-nowrap [&_svg]:size-3',
  {
    variants: {
      variant: {
        default: 'border-transparent bg-primary text-primary-foreground',
        secondary: 'border-transparent bg-secondary text-secondary-foreground',
        outline: 'border-border text-foreground',
        success: 'border-transparent bg-success/15 text-success-text',
        warning: 'border-transparent bg-warning/20 text-foreground',
        destructive: 'border-transparent bg-destructive/15 text-destructive',
        // the status of a thing (see statusFill.ts)
        dirty: statusFill.dirty.fill,
        cleaning: statusFill.cleaning.fill,
        clean: statusFill.clean.fill,
        inspected: statusFill.inspected.fill,
        booked: statusFill.booked.fill,
        inhouse: statusFill.inhouse.fill,
        closed: statusFill.closed.fill,
        ooo: statusFill.ooo.fill,
        draft: statusFill.draft.fill,
        noshow: statusFill.noshow.fill,
        cancelled: statusFill.cancelled.fill,
      },
    },
    defaultVariants: { variant: 'default' },
  },
)

export type BadgeVariants = VariantProps<typeof badgeVariants>
