import { afterEach, describe, expect, it } from 'vitest'
import { setLocale } from '@/i18n'
import { permissionGroupText, permissionText } from './permissions'
import { routeTitle } from './routeTitle'
import { statementTitle } from './statementTitle'

afterEach(() => setLocale('en'))

describe('permissionText', () => {
  it('says what a permission allows in the language of the page, and falls back to the server text', async () => {
    const p = { code: 'frontdesk.checkin', description: 'Check guests in (including walk-ins)' }
    expect(permissionText(p)).toBe('Check guests in (including walk-ins)')
    await setLocale('id')
    expect(permissionText(p)).toBe('Check-in tamu (termasuk tamu langsung)')
    expect(permissionText({ code: 'brand.new', description: 'A new permission' })).toBe('A new permission')
    expect(permissionGroupText('End of day')).toBe('Akhir hari')
    expect(permissionGroupText('Something else')).toBe('Something else')
  })
})

describe('statementTitle', () => {
  it('translates the lines of the financial statements by their English title', async () => {
    expect(statementTitle('Net income')).toBe('Net income')
    await setLocale('id')
    expect(statementTitle('Net income')).toBe('Laba bersih')
    expect(statementTitle('Operating revenue')).toBe('Pendapatan operasional')
    expect(statementTitle("Owner's equity (capital and drawings)")).toBe('Ekuitas pemilik (modal dan prive)')
    expect(statementTitle('Their own line')).toBe('Their own line')
  })
})

describe('routeTitle', () => {
  const route = (path: string, name: string, title: string) => ({ path, name, meta: { title } }) as never

  it('gives a page of the menu the label of the menu, and a page outside it its own title', async () => {
    expect(routeTitle(route('/arrivals', 'arrivals', 'Arrivals'))).toBe('Arrivals')
    expect(routeTitle(route('/stays/5', 'stay', 'Stay'))).toBe('Stay')
    await setLocale('id')
    expect(routeTitle(route('/arrivals', 'arrivals', 'Arrivals'))).toBe('Kedatangan')
    expect(routeTitle(route('/stays/5', 'stay', 'Stay'))).toBe('Menginap')
    expect(routeTitle(route('/account', 'account', 'Account'))).toBe('Akun')
    expect(routeTitle(route('/x', 'not-known', 'Fallback'))).toBe('Fallback')
  })
})
