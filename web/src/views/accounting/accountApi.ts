import { api } from '@/api/client'
import type { GlAccount } from '@/api/types'

/** The chart of a property. The endpoint can also answer CSV, which makes the generated type a union: this fixes it to the JSON answer. */
export async function listAccounts(propertyId: number, query: { postable?: boolean; active?: boolean; account_type?: string } = {}): Promise<GlAccount[]> {
  const get = api.GET as unknown as (path: string, init: object) => Promise<{ data?: { data: GlAccount[] } }>
  const { data } = await get('/api/v1/properties/{propertyId}/accounting/accounts', { params: { path: { propertyId }, query } })
  return data?.data ?? []
}
