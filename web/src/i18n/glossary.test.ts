import fs from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'

/** docs/glossary.md: one Indonesian term for each concept. The words below were replaced; they must not come back in the Indonesian messages. */
const BANNED: [RegExp, string][] = [
  [/\bIn-house\b/i, 'Menginap'],
  [/Night audit/i, 'Audit malam'],
  [/City ledger/i, 'Piutang perusahaan'],
  [/Tape chart/i, 'Bagan kamar'],
  [/Rate plan/i, 'Paket tarif'],
  [/Tahun fiskal/i, 'Tahun buku'],
  [/\bhutang\b/i, 'utang'],
  [/Pengembalian pajak/i, 'SPT Masa PPN'],
  [/Credit note/i, 'Nota kredit'],
]

describe('the glossary of the Indonesian messages', () => {
  it('has none of the English or non-standard terms it replaced', () => {
    const source = fs.readFileSync(path.resolve(__dirname, 'locales', 'id.ts'), 'utf8')
    const hits: string[] = []
    for (const [, key, value] of source.matchAll(/^\s*(\w+): '((?:[^'\\]|\\.)*)',?$/gm)) {
      for (const [banned, use] of BANNED) {
        // a key of the table of error codes or a permission is not a message
        if (banned.test(value ?? '')) hits.push(`${key}: ${value} (use "${use}")`)
      }
    }
    expect(hits).toEqual([])
  })
})
