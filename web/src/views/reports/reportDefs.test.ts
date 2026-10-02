import { describe, expect, it } from 'vitest'
import { setLocale } from '@/i18n'
import { reports } from './reportDefs'

describe('reportDefs', () => {
  it('has a title, a hint and column names in both languages', () => {
    try {
      for (const locale of ['en', 'id'] as const) {
        setLocale(locale)
        for (const def of reports) {
          expect(def.title()).not.toContain('reportDefs.')
          expect(def.hint()).not.toContain('reportDefs.')
        }
      }
      setLocale('id')
      expect(reports.find((x) => x.key === 'maintenance')?.title()).toBe('Pemeliharaan')
      const cashier = reports.find((x) => x.key === 'cashier')!
      const table = cashier.table({ lines: [], net: '0' } as never)
      expect(table.columns[0]).toBe('Tanggal bisnis')
      expect(table.footer?.[0]).toBe('Bersih')
    } finally {
      setLocale('en')
    }
  })
})
