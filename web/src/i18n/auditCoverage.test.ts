import { readdirSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { en } from '@/i18n/locales/en'
import { id } from '@/i18n/locales/id'
import { AUDIT_ACTIONS, AUDIT_ENTITIES } from '@/utils/audit'

/**
 * Every kind of thing (entity) and every action the backend writes into the audit trail has words in both languages, and is offered by the filters. The test reads the Go sources
 * of the modules that write the trail: a module that writes an entity or an action that the screen has no words for fails here, and not in a screenshot ("Bed type created").
 */
const INTERNAL = resolve(__dirname, '../../../internal')

function goFiles(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = resolve(dir, e.name)
    if (e.isDirectory()) return e.name === 'auditdb' ? [] : goFiles(p)
    return e.name.endsWith('.go') && !e.name.endsWith('_test.go') ? [p] : []
  })
}

const permissions = new Set([...readFileSync(resolve(INTERNAL, 'platform/auth/permissions.go'), 'utf8').matchAll(/"([a-z_]+\.[a-z_]+)"/g)].map((m) => m[1]!))

/** The helpers of the modules that make an entry: entry(p, property, date, "action", "entity", id, label, old, new). */
const HELPERS = 'entry|auditEntry|taskAudit|adjustmentAudit|invoiceAudit|reminderAudit'
// Modules whose helper takes the action as a variable: every string like "module.action" in them is an action unless it is a permission.
const ACTION_BY_VARIABLE = ['lostfound/', 'maintenance/', 'folios/fees.go']

function scan(): { actions: Set<string>; entities: Set<string> } {
  const actions = new Set<string>()
  const entities = new Set<string>()
  for (const file of goFiles(INTERNAL)) {
    const rel = file.replace(/\\/g, '/')
    if (rel.includes('/platform/auth/')) continue
    const text = readFileSync(file, 'utf8')
    if (!/audit\.|Audit\(|entry\(|auditEntry\(/.test(text)) continue
    for (const m of text.matchAll(/EntityType:\s*"([a-z_]+)"/g)) entities.add(m[1]!)
    for (const m of text.matchAll(new RegExp(`(?:${HELPERS})\\([^"\\n]*"[a-z_]+\\.[a-z_]+",\\s*"([a-z_]+)"`, 'g'))) entities.add(m[1]!)
    for (const m of text.matchAll(/Action:\s*"([a-z_]+\.[a-z_]+)"/g)) actions.add(m[1]!)
    for (const m of text.matchAll(new RegExp(`(?:${HELPERS})\\([^"\\n]*"([a-z_]+\\.[a-z_]+)"`, 'g'))) actions.add(m[1]!)
    if (ACTION_BY_VARIABLE.some((p) => rel.includes(p))) {
      for (const m of text.matchAll(/"([a-z_]+\.[a-z_]+)"/g)) if (!permissions.has(m[1]!)) actions.add(m[1]!)
    }
  }
  return { actions, entities }
}

const { actions, entities } = scan()
const key = (action: string): string => action.replace('.', '_')

describe('the words of the audit trail', () => {
  it('finds what the backend writes', () => {
    expect(actions.size).toBeGreaterThan(150)
    expect(entities.size).toBeGreaterThan(45)
    expect(actions).toContain('bed_type.created')
    expect(entities).toContain('bed_type')
  })

  it('has words for every entity the backend writes, in English and Indonesian', () => {
    const en_ = en.auditEntity as Record<string, string>
    const id_ = id.auditEntity as Record<string, string>
    expect([...entities].filter((e) => !en_[e]).sort()).toEqual([])
    expect([...entities].filter((e) => !id_[e]).sort()).toEqual([])
  })

  it('has words for every action the backend writes, in English and Indonesian', () => {
    const en_ = en.auditAction as Record<string, string>
    const id_ = id.auditAction as Record<string, string>
    expect([...actions].filter((a) => !en_[key(a)]).sort()).toEqual([])
    expect([...actions].filter((a) => !id_[key(a)]).sort()).toEqual([])
  })

  it('offers every entity and every action of the backend as a filter', () => {
    expect([...entities].filter((e) => !(AUDIT_ENTITIES as readonly string[]).includes(e)).sort()).toEqual([])
    expect([...actions].filter((a) => !(AUDIT_ACTIONS as readonly string[]).includes(a)).sort()).toEqual([])
  })

  it('says a bed type in the words of the glossary: "tempat tidur", never "ranjang"', () => {
    expect(en.auditEntity.bed_type).toBe('Bed type')
    expect(id.auditEntity.bed_type).toBe('Tipe tempat tidur')
    expect(id.auditAction.bed_type_created).toBe('Tipe tempat tidur dibuat')
    expect(id.auditAction.bed_type_updated).toBe('Tipe tempat tidur diubah')
    expect(id.auditAction.bed_types_seeded).toBe('Tipe tempat tidur bawaan dibuat')
    expect(JSON.stringify(id)).not.toMatch(/ranjang/i)
  })
})
