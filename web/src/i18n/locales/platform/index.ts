import { commonEn } from './common.en'
import { commonId } from './common.id'
import { invoicesEn } from './invoices.en'
import { invoicesId } from './invoices.id'
import { loginEn } from './login.en'
import { loginId } from './login.id'
import { plansEn } from './plans.en'
import { plansId } from './plans.id'
import { tenantEn } from './tenant.en'
import { tenantId } from './tenant.id'
import { tenantsEn } from './tenants.en'
import { tenantsId } from './tenants.id'

/**
 * The `platform` namespace of the messages: one file per screen so that they can be written side by side. `platform.<screen>.*` is what a screen reads;
 * `platform.name`, `platform.nav`, `platform.titles` are shared. The Indonesian files are typed against the English ones.
 */
export const platformEn = { ...commonEn, login: loginEn, tenants: tenantsEn, tenant: tenantEn, plans: plansEn, invoices: invoicesEn }
export const platformId: typeof platformEn = { ...commonId, login: loginId, tenants: tenantsId, tenant: tenantId, plans: plansId, invoices: invoicesId }
