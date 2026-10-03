import { i18n, t } from '@/i18n'
import type { components } from './schema'

export type Problem = components['schemas']['Problem']
export type FieldError = components['schemas']['FieldError']

/**
 * ApiError is thrown for every non-2xx API response. It carries the stable
 * `code` from the RFC 9457 problem document, which the UI switches on
 * (never on HTTP status text or messages).
 */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly fieldErrors: FieldError[]
  readonly context: Record<string, unknown>
  readonly retryable: boolean
  readonly requestId?: string
  /** The message as the server wrote it (English, with the specifics of the case). */
  readonly serverMessage: string

  constructor(problem: Problem) {
    const serverMessage = problem.detail ?? problem.title
    super(serverMessage)
    this.serverMessage = serverMessage
    // The message is read when it is shown, so it follows the language of the moment: in English it is the server's
    // own sentence; in another language the sentence of the code, when the language has one.
    Object.defineProperty(this, 'message', { configurable: true, enumerable: false, get: () => this.localizedMessage() })
    this.name = 'ApiError'
    this.status = problem.status
    this.code = problem.code
    this.fieldErrors = problem.errors ?? []
    this.context = problem.context ?? {}
    this.retryable = problem.retryable ?? false
    this.requestId = problem.request_id
  }

  private localizedMessage(): string {
    const locale = i18n.global.locale.value
    const key = `errors.${this.code}`
    return locale !== 'en' && i18n.global.te(key, locale) ? t(key as never) : this.serverMessage
  }

  /** Message for a given input field, if the server rejected it (in the language of the page when it has a text for the code). */
  fieldMessage(field: string): string | undefined {
    const e = this.fieldErrors.find((f) => f.field === field)
    if (!e) return undefined
    const locale = i18n.global.locale.value
    const key = `fieldErrors.${e.code}`
    return locale !== 'en' && i18n.global.te(key, locale) ? t(key as never) : (e.message ?? e.code)
  }
}

function isProblem(value: unknown): value is Problem {
  if (typeof value !== 'object' || value === null) return false
  const v = value as Record<string, unknown>
  return typeof v.code === 'string' && typeof v.status === 'number'
}

/** Converts any failed response body into an ApiError (non-problem bodies included). */
export function toApiError(body: unknown, status: number): ApiError {
  if (isProblem(body)) return new ApiError(body)
  return new ApiError({
    type: 'about:blank',
    title: 'Unexpected response',
    status,
    code: status === 0 ? 'NETWORK_ERROR' : 'UNEXPECTED_RESPONSE',
    detail: status === 0 ? 'The server could not be reached.' : `The server answered with HTTP ${status}.`,
  })
}
