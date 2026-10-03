import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { en } from './locales/en'
import { id } from './locales/id'

/** The Go files of the backend (not the tests): where the error codes are born. */
function goFiles(dir: string): string[] {
  const out: string[] = []
  for (const name of readdirSync(dir)) {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) out.push(...goFiles(path))
    else if (name.endsWith('.go') && !name.endsWith('_test.go')) out.push(path)
  }
  return out
}

const CONSTRUCTOR = /apperr\.(?:New\(\w+\.\w+, |BadRequest\(|Unauthorized\(|Forbidden\(|NotFound\(|Conflict\(|Unavailable\(|Busy\()"([A-Z][A-Z0-9_]+)"/g
const CONSTRAINT_MAP = /\{apperr\.Kind\w+, "([A-Z][A-Z0-9_]+)"/g

describe('error codes', () => {
  it('every code the backend can answer with has a text in both languages', () => {
    const root = resolve(__dirname, '../../../internal')
    const codes = new Set<string>()
    for (const file of goFiles(root)) {
      const text = readFileSync(file, 'utf8')
      for (const re of [CONSTRUCTOR, CONSTRAINT_MAP]) {
        for (const m of text.matchAll(re)) codes.add(m[1]!)
      }
    }
    expect(codes.size).toBeGreaterThan(100) // the scan found the backend
    const missing = (messages: { errors: Record<string, string> }) => [...codes].filter((c) => !(c in messages.errors)).sort()
    // a new error code needs its sentences in locales/en.ts and locales/id.ts (namespace `errors`)
    expect({ en: missing(en), id: missing(id) }).toEqual({ en: [], id: [] })
  })
})
