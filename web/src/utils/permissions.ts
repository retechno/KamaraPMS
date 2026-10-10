import { t, te } from '@/i18n'

/** The words for a permission of a role in the language of the page; the description the server sent is the fallback. */
export function permissionText(p: { code: string; description: string }): string {
  const key = `permissionLabel.${p.code.replace(/\./g, '_')}`
  return te(key) ? t(key as never) : p.description
}

/** The words for the group a permission belongs to ("End of day"). */
export function permissionGroupText(group: string): string {
  const key = `permissionGroup.${group.toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_|_$/g, '')}`
  return te(key) ? t(key as never) : group
}
