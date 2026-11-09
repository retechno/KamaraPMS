import { test, expect } from '../support/fixtures'
import { TENANT } from '../support/env'
import { tx } from '../support/i18n'

/** Sign in, sign out, the redirect after sign-in, and what each desk may open (menus and 403s). */
test.describe('Auth dan hak akses', () => {
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(() => { try { localStorage.setItem('pms.locale', 'id') } catch { /* defaults */ } })
  })

  test('AUTH-01 login lewat layar login dan masuk ke dashboard', async ({ page, seed }) => {
    await page.goto('/login')
    await page.locator('input[name=tenant_code]').fill(TENANT)
    await page.locator('input[name=email]').fill(seed.users.admin.email)
    await page.locator('input[name=password]').fill(seed.users.admin.password)
    await page.getByRole('button', { name: tx('login.signIn') }).click()
    await expect(page).toHaveURL((u) => !u.pathname.startsWith('/login'))
    await expect(page.getByTestId('topbar')).toBeVisible()
    await page.getByTestId('user-menu').click()
    await expect(page.getByTestId('user-email')).toHaveText(seed.users.admin.email)
  })

  test('AUTH-02 password salah menampilkan pesan dan mengosongkan password', async ({ page, seed }) => {
    await page.goto('/login')
    await page.locator('input[name=tenant_code]').fill(TENANT)
    await page.locator('input[name=email]').fill(seed.users.frontdesk.email)
    await page.locator('input[name=password]').fill('salah-sekali-12345')
    await page.getByRole('button', { name: tx('login.signIn') }).click()
    await expect(page.getByTestId('login-error')).toHaveText(tx('login.INVALID_CREDENTIALS'))
    await expect(page.locator('input[name=password]')).toHaveValue('')
    await expect(page).toHaveURL(/\/login/)
  })

  test('AUTH-03 alamat yang dibuka sebelum login dibuka lagi sesudah login', async ({ page, seed }) => {
    await page.goto('/reservations')
    await expect(page).toHaveURL(/\/login\?redirect=(%2F|\/)reservations/) // vue-router leaves the slash of the path unencoded
    await page.locator('input[name=tenant_code]').fill(TENANT)
    await page.locator('input[name=email]').fill(seed.users.frontdesk.email)
    await page.locator('input[name=password]').fill(seed.users.frontdesk.password)
    await page.getByRole('button', { name: tx('login.signIn') }).click()
    await expect(page).toHaveURL(/\/reservations$/)
  })

  test('AUTH-04 logout kembali ke layar login dan sesi tidak bisa dipakai lagi', async ({ page, loginAs }) => {
    await loginAs('frontdesk')
    await page.getByTestId('user-menu').click()
    await page.getByTestId('sign-out').click()
    await expect(page).toHaveURL(/\/login/)
    await page.goto('/in-house')
    await expect(page).toHaveURL(/\/login/)
  })

  test('AUTH-05 sesi bertahan setelah halaman dimuat ulang (refresh cookie)', async ({ page, loginAs }) => {
    await loginAs('frontdesk', '/arrivals')
    await page.reload()
    await expect(page).toHaveURL(/\/arrivals/)
    await expect(page.getByTestId('topbar')).toBeVisible()
  })

  test('AUTH-06 housekeeping tidak melihat menu akuntansi dan alamatnya dikembalikan ke beranda', async ({ page, loginAs }) => {
    await loginAs('housekeeping')
    await expect(page.getByTestId('sidebar-nav')).toBeVisible()
    await expect(page.getByTestId('section-accounting')).toHaveCount(0)
    await expect(page.getByTestId('section-setup')).toHaveCount(0)
    // the journals page is open to every signed-in desk and says itself who may read it (it needs accounting.view)
    await page.goto('/accounting/journals')
    await expect(page.getByTestId('no-access')).toContainText('accounting.view')
    await page.goto('/setup/users')
    await expect(page).toHaveURL((u) => u.pathname === '/')
  })

  test('AUTH-07 API menolak resepsionis menjalankan night audit (403 PERMISSION_DENIED)', async ({ apiAs, seed, data }) => {
    const fd = await apiAs('frontdesk')
    const bd = await data.businessDate()
    const err = await fd.post(`/properties/${seed.propertyId}/night-audit/run`, { business_date: bd }).catch((e) => e)
    expect(err.status).toBe(403)
    expect(err.code).toBe('PERMISSION_DENIED')
  })

  test('AUTH-08 resepsionis melihat menu front office tetapi tidak melihat setup', async ({ page, loginAs }) => {
    await loginAs('frontdesk')
    await expect(page.getByTestId('section-frontDesk')).toBeVisible()
    await expect(page.getByTestId('section-setup')).toHaveCount(0)
    await expect(page.getByTestId('section-accounting')).toHaveCount(0)
  })

  test('AUTH-09 ganti bahasa ke English dari menu pengguna', async ({ page, loginAs }) => {
    await loginAs('frontdesk')
    await page.getByTestId('user-menu').click()
    await page.getByTestId('language-en').click()
    await expect(page.locator('html')).toHaveAttribute('lang', 'en')
  })
})
