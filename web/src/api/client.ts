import { userActorIds } from '@/lib/actors'
import type { Actor, Agenda, ErrorDetail, EventListResponse, Issue, Page, Session, VerificationState } from './types'

export interface ClientConfig {
  /** 인스턴스 기준 경로. 개발은 '/api/local' 같은 프록시 접두어, 운영은 '' (같은 출처). */
  baseUrl: string
  token: string
  /** 없으면 X-Actor를 보내지 않는다. 사용자 행위자를 알기 전 목록을 받을 때만 비운다. */
  actor?: string
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

export function isAbortError(err: unknown): boolean {
  return (
    (err instanceof DOMException && err.name === 'AbortError') ||
    (typeof err === 'object' && err !== null && (err as { name?: unknown }).name === 'AbortError')
  )
}

export function isPreconditionFailed(err: unknown): boolean {
  return isApiError(err) && err.status === 412
}

/** 저장된 행위자를 서버가 거절한 오류: 선언되지 않은 행위자(400)나 토큰과 맞지 않는 행위자(403). */
export function isActorRejected(err: unknown): boolean {
  return (
    isApiError(err) &&
    ((err.status === 400 && err.details.some((d) => d.reason === 'unknown_actor')) ||
      (err.status === 403 && err.details.some((d) => d.reason === 'actor_mismatch')))
  )
}

export function isNotFound(err: unknown): boolean {
  return isApiError(err) && err.status === 404
}

/** 이미 승인되거나 거부된 기기 승인 요청을 바꾸려 한 오류(409 conflict, `details[].reason`이 not_pending). */
export function isNotPending(err: unknown): boolean {
  return isApiError(err) && err.status === 409 && err.code === 'conflict' && err.details.some((d) => d.reason === 'not_pending')
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
  /** 마일스톤 ID(문자열)와 none. 쉼표 OR. none은 마일스톤이 없는 이슈다. */
  milestone?: readonly string[]
  /** 검증 상태 필터(none, pending, verified). 배열이면 쉼표 OR. 없으면 거르지 않는다. */
  verification?: VerificationState | readonly VerificationState[]
  /** 'true'면 30일 넘게 활동이 없는 열린 이슈만. 없으면 거르지 않는다. */
  dormant?: 'true'
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
    milestone: f.milestone?.join(','),
    verification: Array.isArray(f.verification) ? f.verification.join(',') : f.verification,
    dormant: f.dormant,
    q: f.q.trim(),
    sort: f.sort,
    limit: f.limit,
    offset: f.offset,
  })
}

export function headersFor(config: ClientConfig): Record<string, string> {
  const headers: Record<string, string> = {
    Authorization: `Bearer ${config.token}`,
    'X-Session-Id': config.sessionId,
    'X-Client-Host': config.clientHost,
  }
  if (config.actor !== undefined) headers['X-Actor'] = config.actor
  return headers
}

export interface SessionListParams {
  project?: string
  actor?: string
  parent?: string
  active?: boolean
  named?: boolean
  waiting?: boolean
  limit?: number
  offset?: number
}

export interface AgendaParams {
  project?: string
  limit?: number
}

export interface EventListParams {
  after?: number
  project?: string
  kind?: string
  limit?: number
}

export interface EventStreamParams {
  after?: number
  project?: string
  kind?: string
  lastEventId?: string
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

  verifyIssue(key: string, version?: number): Promise<Issue> {
    return this.post<Issue>(`/issues/${key}/verify`, undefined, version !== undefined ? { ifMatch: version } : {})
  }

  unverifyIssue(key: string, version?: number): Promise<Issue> {
    return this.delete<Issue>(`/issues/${key}/verify`, version !== undefined ? { ifMatch: version } : {})
  }

  listSessions(params?: SessionListParams, signal?: AbortSignal): Promise<Page<Session>> {
    return this.get<Page<Session>>('/sessions', params ? { ...params } : undefined, signal)
  }

  getSession(actor: string, sessionId: string, signal?: AbortSignal): Promise<Session> {
    return this.get<Session>(`/sessions/${encodeURIComponent(actor)}/${encodeURIComponent(sessionId)}`, undefined, signal)
  }

  getAgenda(params?: AgendaParams, signal?: AbortSignal): Promise<Agenda> {
    return this.get<Agenda>('/agenda', params ? { ...params } : undefined, signal)
  }

  listEvents(params?: EventListParams, signal?: AbortSignal): Promise<EventListResponse> {
    return this.get<EventListResponse>('/events', params ? { ...params } : undefined, signal)
  }

  async openEventStream(params?: EventStreamParams, signal?: AbortSignal): Promise<Response> {
    const query = buildQuery({
      after: params?.after,
      project: params?.project,
      kind: params?.kind,
    })
    const url = `${this.config.baseUrl}/v1/events/stream${query}`
    const headers: Record<string, string> = {
      ...headersFor(this.config),
      Accept: 'text/event-stream',
    }
    if (params?.lastEventId) headers['Last-Event-ID'] = params.lastEventId

    let res: Response
    try {
      res = await this.fetchFn(url, { method: 'GET', headers, signal })
    } catch (e) {
      if (isAbortError(e)) throw e
      throw new NetworkError(e)
    }
    if (!res.ok) return parseErrorResponse(res)
    if (!res.body) throw new ServerUnavailableError(res.status)
    return res
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
      if (isAbortError(e)) throw e
      throw new NetworkError(e)
    }
    return parseResponse<T>(res)
  }
}

async function parseErrorResponse(res: Response): Promise<never> {
  if (res.status === 502 || res.status === 503 || res.status === 504) throw new ServerUnavailableError(res.status)
  const contentType = res.headers.get('content-type') ?? ''
  if (!contentType.includes('application/json')) throw new ServerUnavailableError(res.status)
  const data: unknown = await res.json()
  const err = (data as { error?: { code?: string; message?: string; details?: ErrorDetail[] } } | null)?.error
  throw new ApiError(res.status, err?.code ?? 'unknown', err?.message ?? `HTTP ${res.status}`, err?.details ?? [])
}

async function parseResponse<T>(res: Response): Promise<T> {
  if (res.status === 204) return undefined as T
  if (!res.ok) return parseErrorResponse(res)
  const contentType = res.headers.get('content-type') ?? ''
  if (!contentType.includes('application/json')) return undefined as T
  return (await res.json()) as T
}

/** 공용 토큰으로 서버에 선언된 사용자 행위자의 ID를 받는다. 행위자를 정하기 전이라 X-Actor를 보내지 않는다. */
export async function fetchUserActorIds(config: Omit<ClientConfig, 'actor'>, fetchFn?: FetchFn): Promise<string[]> {
  const { items } = await new ApiClient(config, fetchFn).get<{ items: Actor[] }>('/actors')
  return userActorIds(items)
}
