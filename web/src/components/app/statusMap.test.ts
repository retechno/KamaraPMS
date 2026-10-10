import fs from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'
import { STATUS_VARIANTS, statusFill } from '@/components/ui/badge/statusFill'
import { OCCUPANCY_ICON, statusLegend, statusSwatch, statusVariant, type StatusDomain } from './statusMap'

const STATUSES: Record<StatusDomain, string[]> = {
  housekeeping: ['DIRTY', 'CLEANING', 'CLEAN', 'INSPECTED', 'BLOCKED'],
  occupancy: ['OCCUPIED', 'RESERVED', 'VACANT', 'BLOCKED'],
  reservation: ['DRAFT', 'CONFIRMED', 'RESERVED', 'IN_HOUSE', 'CHECKED_IN', 'CHECKED_OUT', 'COMPLETED', 'NO_SHOW', 'CANCELLED'],
  stay: ['OPEN', 'CHECKED_OUT', 'CANCELLED'],
  record: ['OPEN', 'CLOSED'],
  payment: ['POSTED', 'VOIDED'],
  work: ['OPEN', 'IN_PROGRESS', 'RESOLVED', 'CANCELLED'],
  reconciliation: ['OPEN', 'RECONCILED'],
  task: ['PENDING', 'IN_PROGRESS', 'DONE', 'SKIPPED'],
  block: ['OOO', 'OOS'],
}

describe('statusMap', () => {
  it('has no status that looks like an action: none is the default (teal) badge', () => {
    for (const [domain, list] of Object.entries(STATUSES) as [StatusDomain, string[]][]) {
      for (const s of list) expect(statusVariant(domain, s), `${domain} ${s}`).not.toBe('default')
    }
  })

  it('maps the statuses of the design: dirty, cleaning, clean, inspected, booked, in house, closed, out of order', () => {
    expect(statusVariant('housekeeping', 'DIRTY')).toBe('dirty')
    expect(statusVariant('housekeeping', 'CLEANING')).toBe('cleaning')
    expect(statusVariant('housekeeping', 'CLEAN')).toBe('clean')
    expect(statusVariant('housekeeping', 'INSPECTED')).toBe('inspected')
    expect(statusVariant('housekeeping', 'BLOCKED')).toBe('ooo')
    for (const s of ['CONFIRMED', 'RESERVED']) expect(statusVariant('reservation', s)).toBe('booked')
    for (const s of ['IN_HOUSE', 'CHECKED_IN']) expect(statusVariant('reservation', s)).toBe('inhouse')
    for (const s of ['CHECKED_OUT', 'COMPLETED']) expect(statusVariant('reservation', s)).toBe('closed')
    expect(statusVariant('stay', 'OPEN')).toBe('inhouse')
    expect(statusVariant('work', 'IN_PROGRESS')).toBe('cleaning')
    expect(statusVariant('record', 'CLOSED')).toBe('closed')
    expect(statusVariant('block', 'OOO')).toBe('ooo')
  })

  it('shows occupancy as an icon and a word, with no colour of its own', () => {
    for (const s of STATUSES.occupancy) {
      expect(statusVariant('occupancy', s)).toBe('outline')
      expect(OCCUPANCY_ICON[s], s).toBeDefined()
      expect(statusSwatch('occupancy', s).fill).toContain('bg-card')
    }
  })

  it('gives a tile, a bar and a legend the look of a status, and a plain one the neutral look', () => {
    expect(statusSwatch('housekeeping', 'DIRTY')).toEqual(statusFill.dirty)
    expect(statusSwatch('reservation', 'CHECKED_IN')).toEqual(statusFill.inhouse)
    expect(statusSwatch('payment', 'POSTED').fill).toContain('bg-card') // "success" is not one of the status looks
    expect(statusSwatch('housekeeping', 'UNKNOWN').fill).toContain('bg-card')
  })

  it('builds a legend from the map: one entry for statuses that look the same', () => {
    expect(statusLegend('housekeeping').map((e) => e.status)).toEqual(['DIRTY', 'CLEANING', 'CLEAN', 'INSPECTED', 'BLOCKED'])
    expect(statusLegend('reservation', ['CONFIRMED', 'RESERVED', 'CHECKED_IN']).map((e) => e.status)).toEqual(['CONFIRMED', 'CHECKED_IN'])
    expect(statusLegend('housekeeping')[0]?.swatch).toBe(statusFill.dirty.swatch)
  })

  it('has a look for every status variant, with its classes written out so that Tailwind finds them', () => {
    for (const v of STATUS_VARIANTS) {
      const f = statusFill[v]
      for (const cls of [f.fill, f.swatch, f.edge, f.dot].join(' ').split(' ')) expect(cls).toMatch(/^[a-z0-9:/[\]_.-]+$/)
      expect(f.edge).toMatch(/^border-l-status-/)
      expect(f.dot).toMatch(/^bg-status-/)
    }
  })

  it('is not bypassed by a view that gives a status the default (teal) badge', () => {
    const root = path.resolve(__dirname, '../..')
    const hits: string[] = []
    const walk = (dir: string): void => {
      for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
        const p = path.join(dir, e.name)
        if (e.isDirectory()) walk(p)
        else if (e.name.endsWith('.vue')) {
          for (const [i, line] of fs.readFileSync(p, 'utf8').split('\n').entries()) {
            if (/<Badge\b/.test(line) && /variant="default"|'default'/.test(line)) hits.push(`${path.relative(root, p)}:${i + 1}`)
          }
        }
      }
    }
    walk(path.join(root, 'views'))
    walk(path.join(root, 'components'))
    expect(hits).toEqual([])
  })
})
