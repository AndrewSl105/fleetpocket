export class ApiError extends Error {
  readonly status: number
  readonly code: string
  /** Seconds from Retry-After, when the server sent it. */
  readonly retryAfter?: number
  /** `409 merge_conflict` (branch merge): the files the merge could not combine. */
  readonly files?: string[]

  constructor(status: number, code: string, message: string, retryAfter?: number, files?: string[]) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.retryAfter = retryAfter
    this.files = files
  }
}

export function parseRetryAfter(value: string | null): number | undefined {
  if (!value) return undefined
  const seconds = Number(value)
  if (Number.isFinite(seconds)) return Math.max(0, seconds)
  const date = Date.parse(value)
  if (Number.isNaN(date)) return undefined
  return Math.max(0, Math.round((date - Date.now()) / 1000))
}

export async function toApiError(response: Response, body: unknown): Promise<ApiError> {
  const err = (body as { error?: { code?: string; message?: string; files?: unknown } } | undefined)?.error
  const files = Array.isArray(err?.files) ? err.files.filter((f): f is string => typeof f === 'string') : undefined
  return new ApiError(
    response.status,
    err?.code ?? `http_${response.status}`,
    err?.message ?? (response.statusText || 'Request failed'),
    parseRetryAfter(response.headers.get('Retry-After')),
    files,
  )
}

export function isApiError(e: unknown, code?: string): e is ApiError {
  return e instanceof ApiError && (code === undefined || e.code === code)
}
