import { randomUUID } from 'node:crypto'
import { API, TENANT } from './env'

/** A failed API call, with the stable error code of the server (apperr) so a test can tell a 409 it expects from a bug. */
export class ApiCallError extends Error {
  constructor(readonly method: string, readonly path: string, readonly status: number, readonly code: string, readonly body: unknown) {
    super(`${method} ${path} -> ${status} ${code} ${typeof body === 'object' ? JSON.stringify(body).slice(0, 400) : String(body).slice(0, 400)}`)
  }
}

type Query = Record<string, string | number | boolean | undefined>

/**
 * The API seen by a signed-in user, for preparing data and checking results. Tests drive the screens through the browser;
 * this client only makes what a step needs beforehand and reads the database afterwards (never a substitute for the step under test).
 */
export class ApiClient {
  private constructor(readonly token: string, readonly userId: number, readonly email: string) {}

  static async login(email: string, password: string, tenant = TENANT): Promise<ApiClient> {
    const r = await fetch(`${API}/auth/login`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ tenant_code: tenant, email, password }) })
    const j = (await r.json().catch(() => ({}))) as { access_token?: string; user?: { id: number }; code?: string }
    if (!r.ok || !j.access_token) throw new ApiCallError('POST', '/auth/login', r.status, j.code ?? '', j)
    return new ApiClient(j.access_token, j.user?.id ?? 0, email)
  }

  async call<T = any>(method: string, path: string, body?: unknown, query?: Query): Promise<T> {
    const qs = query ? '?' + new URLSearchParams(Object.entries(query).filter(([, v]) => v !== undefined).map(([k, v]) => [k, String(v)])).toString() : ''
    const headers: Record<string, string> = { authorization: `Bearer ${this.token}` }
    if (body !== undefined) headers['content-type'] = 'application/json'
    if (method !== 'GET') headers['idempotency-key'] = randomUUID()
    const r = await fetch(API + path + qs, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) })
    const text = await r.text()
    let json: unknown = text
    try { json = text ? JSON.parse(text) : null } catch { /* not JSON (a PDF, an empty 204) */ }
    if (!r.ok) throw new ApiCallError(method, path, r.status, (json as { code?: string })?.code ?? '', json)
    return json as T
  }

  get<T = any>(path: string, query?: Query) { return this.call<T>('GET', path, undefined, query) }
  post<T = any>(path: string, body: unknown = {}) { return this.call<T>('POST', path, body) }
  patch<T = any>(path: string, body: unknown) { return this.call<T>('PATCH', path, body) }
  put<T = any>(path: string, body: unknown) { return this.call<T>('PUT', path, body) }

  /** The same call, but a failure returns null (data that may already exist, a step that may not apply). */
  async soft<T = any>(method: string, path: string, body?: unknown): Promise<T | null> {
    try { return await this.call<T>(method, path, body) } catch { return null }
  }

  /** Every row of a list endpoint ({ data, next_cursor }). */
  async all<T = any>(path: string, query: Query = {}): Promise<T[]> {
    const out: T[] = []
    let cursor: string | undefined
    for (let i = 0; i < 50; i++) {
      const page = await this.get<{ data: T[]; next_cursor?: string }>(path, { limit: 200, ...query, cursor })
      out.push(...(page.data ?? []))
      if (!page.next_cursor) break
      cursor = page.next_cursor
    }
    return out
  }
}
