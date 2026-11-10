/** Where the e2e stack runs and who signs in. scripts/e2e.sh exports the same names; these defaults match it. */
export const E2E_API_ADDR = process.env.PMS_E2E_HTTP_ADDR ?? '127.0.0.1:18090'
export const API_ORIGIN = `http://${E2E_API_ADDR}`
export const API = `${API_ORIGIN}/api/v1`
export const WEB_ORIGIN = process.env.E2E_WEB ?? 'http://localhost:5174'

export const TENANT = process.env.E2E_TENANT ?? 'E2E'
export const PROPERTY_CODE = process.env.E2E_PROPERTY ?? 'E2E'
export const ADMIN = { email: process.env.E2E_ADMIN_EMAIL ?? 'admin@e2e.example', password: process.env.E2E_ADMIN_PASSWORD ?? 'E2e-Admin-Passw0rd!' }
export const USER_PASSWORD = process.env.E2E_USER_PASSWORD ?? 'E2e-User-Passw0rd!'
export const TZ = 'Asia/Makassar'

/** The file the setup project writes and every test reads (ids of what the seed made). */
export const STATE_FILE = new URL('../.state/seed.json', import.meta.url)
