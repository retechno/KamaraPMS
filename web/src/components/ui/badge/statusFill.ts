/**
 * How a status looks, by name. The class names are written out in full so that Tailwind finds them. A status is a colour AND a weight: a light fill waits ("confirmed", "dirty"), a solid
 * fill is under way or ready ("in house", "inspected"), and a stripe is "not available" (out of order). Teal is not here: it is the colour of actions.
 */
export const STATUS_VARIANTS = ['dirty', 'cleaning', 'clean', 'inspected', 'booked', 'inhouse', 'closed', 'ooo', 'draft', 'noshow', 'cancelled'] as const
export type StatusVariant = (typeof STATUS_VARIANTS)[number]

export interface StatusFill {
  /** Background, text and border of a badge, a tile or a bar. */
  fill: string
  /** A small swatch of the fill, for a legend (no text colour). */
  swatch: string
  /** The colour bar on the left of a tile that has a neutral background. */
  edge: string
  /** A dot of the colour of the status. */
  dot: string
}

export const statusFill: Record<StatusVariant, StatusFill> = {
  dirty: { fill: 'border-status-dirty-line bg-status-dirty-bg text-status-dirty', swatch: 'border-status-dirty-line bg-status-dirty-bg', edge: 'border-l-status-dirty', dot: 'bg-status-dirty' },
  cleaning: { fill: 'border-status-cleaning-line bg-status-cleaning-bg text-status-cleaning', swatch: 'border-status-cleaning-line bg-status-cleaning-bg', edge: 'border-l-status-cleaning', dot: 'bg-status-cleaning' },
  clean: { fill: 'border-status-clean-line bg-status-clean-bg text-status-clean', swatch: 'border-status-clean-line bg-status-clean-bg', edge: 'border-l-status-clean', dot: 'bg-status-clean' },
  inspected: { fill: 'border-status-inspected bg-status-inspected text-status-inspected-fg', swatch: 'border-status-inspected bg-status-inspected', edge: 'border-l-status-inspected', dot: 'bg-status-inspected' },
  booked: { fill: 'border-status-booked-line bg-status-booked-bg text-status-booked', swatch: 'border-status-booked-line bg-status-booked-bg', edge: 'border-l-status-booked', dot: 'bg-status-booked' },
  inhouse: { fill: 'border-status-inhouse bg-status-inhouse text-status-inhouse-fg', swatch: 'border-status-inhouse bg-status-inhouse', edge: 'border-l-status-inhouse', dot: 'bg-status-inhouse' },
  closed: { fill: 'border-status-closed-line bg-status-closed-bg text-status-closed', swatch: 'border-status-closed-line bg-status-closed-bg', edge: 'border-l-status-closed', dot: 'bg-status-closed' },
  ooo: { fill: 'status-hatch border-status-ooo text-status-ooo-fg', swatch: 'status-hatch border-status-ooo', edge: 'border-l-status-ooo', dot: 'bg-status-ooo' },
  draft: { fill: 'border-dashed border-status-draft-line bg-status-draft-bg text-status-draft', swatch: 'border-dashed border-status-draft-line bg-status-draft-bg', edge: 'border-l-status-draft-line', dot: 'bg-status-draft-line' },
  noshow: { fill: 'border-status-noshow-line bg-status-noshow-bg text-status-noshow', swatch: 'border-status-noshow-line bg-status-noshow-bg', edge: 'border-l-status-noshow', dot: 'bg-status-noshow' },
  cancelled: { fill: 'border-status-cancelled-line bg-status-cancelled-bg text-status-cancelled', swatch: 'border-status-cancelled-line bg-status-cancelled-bg', edge: 'border-l-status-cancelled', dot: 'bg-status-cancelled' },
}
