import { describe, expect, it } from 'vitest'
import { en } from '@/i18n/locales/en'
import { id } from '@/i18n/locales/id'
import { AUDIT_ACTIONS, AUDIT_ENTITIES } from './audit'

describe('the audit filters', () => {
  it('have words, in both languages, for every action and entity they offer', () => {
    for (const messages of [en, id]) {
      for (const action of AUDIT_ACTIONS) expect(messages.auditAction, action).toHaveProperty(action.replace('.', '_'))
      for (const entity of AUDIT_ENTITIES) expect(messages.auditEntity, entity).toHaveProperty(entity)
    }
  })

  it('offer every action and entity that has words', () => {
    const offered = new Set(AUDIT_ACTIONS.map((a) => a.replace('.', '_')))
    expect(Object.keys(en.auditAction).filter((k) => !offered.has(k))).toEqual([])
    expect(Object.keys(en.auditEntity).filter((k) => !(AUDIT_ENTITIES as readonly string[]).includes(k))).toEqual([])
  })

  it('list each code once', () => {
    expect(new Set(AUDIT_ACTIONS).size).toBe(AUDIT_ACTIONS.length)
    expect(new Set(AUDIT_ENTITIES).size).toBe(AUDIT_ENTITIES.length)
  })
})
