import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { en } from '@/i18n/locales/en'
import { id } from '@/i18n/locales/id'

// The permissions of the inventory, as the server declares them (internal/platform/auth/permissions.go).
const inventory = [...readFileSync(resolve(__dirname, '../../../internal/platform/auth/permissions.go'), 'utf8').matchAll(/"(inventory\.[a-z_]+)"/g)].map((m) => m[1]!)

describe('the words of the permissions', () => {
  // Item 8 of PR C2: 11 of the 14 inventory permissions had no label, so the role screen showed the English description of the server to an Indonesian administrator.
  it('names every inventory permission in English and in Indonesian', () => {
    expect(inventory.length).toBe(14)
    for (const code of inventory) {
      const key = code.replace(/\./g, '_')
      expect((en.permissionLabel as Record<string, string>)[key], `en ${code}`).toBeTruthy()
      expect((id.permissionLabel as Record<string, string>)[key], `id ${code}`).toBeTruthy()
    }
  })
})
