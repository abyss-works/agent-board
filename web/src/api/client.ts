import type { ErrorDetail } from './types'

export interface ClientConfig {
  /** 인스턴스 기준 경로. 개발은 '/api/local' 같은 프록시 접두어, 운영은 '' (같은 출처). */
  baseUrl: string
  token: string
  actor: string
  sessionId: string
  clientHost: string
}

export type QueryValue = string | number | boolean | null | undefined | readonly string[]
export type QueryParams = Record<string, QueryValue>

/** 서버가 돌려준 오류 응답 `{error:{code,message,details}}`. */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly details: ErrorDetail[]

  constructor(status: number, code: string, message: string, details: ErrorDetail[] = []) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.details = details
  }

  /** 특정 필드의 첫 번째 세부 메시지. 없으면 null. */
  detailFor(field: string): string | null {
    const d = this.details.find((x) => x.field === field)
    return d?.message ?? d?.reason ?? null
  }
}

/** fetch 자체가 실패한 경우(브라우저가 개발 서버·호스트에 닿지 못함). */
export class NetworkError extends Error {
  constructor(cause?: unknown) {
    super('네트워크 오류로 요청을 보내지 못했습니다.', { cause })
    this.name = 'NetworkError'
  }
}

/** 게이트웨이가 502/503/504를 돌려주거나 응답이 JSON이 아닐 때. 서버가 꺼져 있다고 본다. */
export class ServerUnavailableError extends Error {
  readonly status: number

  constructor(status: number) {
    super('서버가 꺼져 있거나 응답하지 않습니다.')
    this.name = 'ServerUnavailableError'
    this.status = status
  }
}

export function isApiError(err: unknown): err is ApiError {
  return err instanceof ApiError
}

export function isUnauthorized(err: unknown): boolean {
  return isApiError(err) && err.status === 401
}

export function isPreconditionFailed(err: unknown): boolean {
  return isApiError(err) && err.status === 412
}

/** 사람이 읽을 오류 문구. */
export function errorMessage(err: unknown): string {
  if (err instanceof ApiError || err instanceof NetworkError || err instanceof ServerUnavailableError) return err.message
  if (err instanceof Error) return err.message
  return String(err)
}

/**
 * 질의 문자열을 만든다. 빈 문자열, null, undefined는 건너뛰고 배열은 같은 이름을 반복한다(AND).
 * 쉼표 OR이 필요하면 호출자가 미리 이어 붙인다.
 */
export function buildQuery(params: QueryParams): string {
  const search = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === '') continue
    if (Array.isArray(value)) {
      for (const v of value) search.append(key, v)
    } else {
      search.append(key, String(value))
    }
  }
  const s = search.toString()
  return s ? `?${s}` : ''
}

export interface IssueListFilters {
  project: string | null
  status: readonly string[]
  type: readonly string[]
  priority: readonly string[]
  label: readonly string[]
  /** 주제 ID(문자열)와 none. 쉼표 OR. 대주제 ID는 그 아래 주제의 이슈도 포함한다. */
  topic?: readonly string[]
  /** 'false'면 미확인만, 'true'면 확인된 것만. 없으면 거르지 않는다. */
  acknowledged?: 'true' | 'false'
  q: string
  sort: string
  limit?: number
  offset?: number
}

/** GET /v1/issues 질의. 상태·종류·우선순위는 쉼표 OR, 라벨은 반복 AND다. */
export function issueListQuery(f: IssueListFilters): string {
  return buildQuery({
    project: f.project,
    status: f.status.join(','),
    type: f.type.join(','),
    priority: f.priority.join(','),
    label: f.label,
    topic: f.topic?.join(','),
    acknowledged: f.acknowledged,
    q: f.q.trim(),
    sort: f.sort,
    limit: f.limit,
    offset: f.offset,
  })
}

export function headersFor(config: ClientConfig): Record<string, string> {
  return {
    Authorization: `Bearer ${config.token}`,
    'X-Actor': config.actor,
    'X-Session-Id': config.sessionId,
    'X-Client-Host': config.clientHost,
  }
}

export interface RequestOptions {
  query?: QueryParams
  body?: unknown
  /** 이슈의 version. `If-Match: "<version>"`으로 보낸다. */
  ifMatch?: number
  signal?: AbortSignal
}

export type FetchFn = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>

export class ApiClient {
  private readonly config: ClientConfig
  private readonly fetchFn: FetchFn

  constructor(config: ClientConfig, fetchFn: FetchFn = (input, init) => fetch(input, init)) {
    this.config = config
    this.fetchFn = fetchFn
  }

  get<T>(path: string, query?: QueryParams, signal?: AbortSignal): Promise<T> {
    return this.request<T>('GET', path, { query, signal })
  }

  post<T>(path: string, body: unknown, opts: Omit<RequestOptions, 'body'> = {}): Promise<T> {
    return this.request<T>('POST', path, { ...opts, body })
  }

  patch<T>(path: string, body: unknown, opts: Omit<RequestOptions, 'body'> = {}): Promise<T> {
    return this.request<T>('PATCH', path, { ...opts, body })
  }

  delete<T>(path: string, opts: RequestOptions = {}): Promise<T> {
    return this.request<T>('DELETE', path, opts)
  }

  async request<T>(method: string, path: string, opts: RequestOptions = {}): Promise<T> {
    const url = `${this.config.baseUrl}/v1${path}${buildQuery(opts.query ?? {})}`
    const headers = headersFor(this.config)
    if (opts.body !== undefined) headers['Content-Type'] = 'application/json; charset=utf-8'
    if (opts.ifMatch !== undefined) headers['If-Match'] = `"${opts.ifMatch}"`

    let res: Response
    try {
      res = await this.fetchFn(url, {
        method,
        headers,
        body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
        signal: opts.signal,
      })
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') throw e
      throw new NetworkError(e)
    }
    return parseResponse<T>(res)
  }
}

async function parseResponse<T>(res: Response): Promise<T> {
  if (res.status === 204) return undefined as T
  const contentType = res.headers.get('content-type') ?? ''
  const isJson = contentType.includes('application/json')
  if (res.status === 502 || res.status === 503 || res.status === 504) throw new ServerUnavailableError(res.status)
  if (!isJson) {
    if (res.ok) return undefined as T
    throw new ServerUnavailableError(res.status)
  }
  const data: unknown = await res.json()
  if (res.ok) return data as T
  const err = (data as { error?: { code?: string; message?: string; details?: ErrorDetail[] } } | null)?.error
  throw new ApiError(res.status, err?.code ?? 'unknown', err?.message ?? `HTTP ${res.status}`, err?.details ?? [])
}
