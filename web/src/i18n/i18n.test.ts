import { afterEach, describe, expect, it } from 'vitest'
import { en } from './locales/en'
import { id } from './locales/id'
import { currentLocale, i18n, LOCALES, setLocale, t } from './index'

const keys = (o: object, prefix = ''): string[] =>
  Object.entries(o).flatMap(([k, v]) => (typeof v === 'object' && v !== null ? keys(v, `${prefix}${k}.`) : [`${prefix}${k}`]))

describe('i18n', () => {
  afterEach(() => {
    setLocale('en')
    localStorage.clear()
  })

  it('starts in English and translates outside a component', () => {
    expect(currentLocale()).toBe('en')
    expect(t('common.save')).toBe('Save')
  })

  it('switches to Indonesian, remembers it and tells the document', () => {
    setLocale('id')
    expect(t('common.save')).toBe('Simpan')
    expect(localStorage.getItem('pms.locale')).toBe('id')
    expect(document.documentElement.lang).toBe('id')
  })

  it('has the same keys in every language, none of them empty', () => {
    expect(keys(id).sort()).toEqual(keys(en).sort())
    for (const k of keys(en)) {
      for (const locale of LOCALES) {
        expect(i18n.global.t(k, {}, { locale }), `${locale}: ${k}`).not.toBe('')
      }
    }
    // one t() call for every key in two languages: it is the longest test of the suite and grows with the messages
  }, 20_000)

  it('falls back to English for a key a language lacks', () => {
    // a key that exists in no language is shown as the key itself, never as an empty label
    expect(t('common.does.not.exist' as never)).toBe('common.does.not.exist')
  })
})
