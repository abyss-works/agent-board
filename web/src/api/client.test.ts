import { describe, expect, it } from 'vitest'
import {
  ApiClient,
  ApiError,
  NetworkError,
  ServerUnavailableError,
  buildQuery,
  headersFor,
  isPreconditionFailed,
  isUnauthorized,
  issueListQuery,
  type ClientConfig,
} from './client'

const config: ClientConfig = {
  baseUrl: '/api/staging',
  token: 'secret-token-0123456789abcdef0123456789',
  actor: 'owner',
  sessionId: 'web-0123456789abcdef',
  clientHost: 'web',
}

interface Captured {
  url: string
  init: RequestInit
}

function fakeFetch(status: number, body: unknown, headers: Record<string, string> = {}) {
  const calls: Captured[] = []
  const fetchFn = async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push({ url: String(input), init: init ?? {} })
    const text = typeof body === 'string' ? body : JSON.stringify(body)
    return new Response(status === 204 ? null : text, {
      status,
      headers: { 'content-type': 'application/json; charset=utf-8', ...headers },
    })
  }
  return { calls, fetchFn }
}

describe('buildQuery', () => {
  it('빈 값과 undefined를 건너뛰고 배열은 같은 이름을 반복한다', () => {
    expect(buildQuery({ project: 'AGB', q: '', sort: undefined, limit: 20, label: ['api', 'cli'] })).toBe(
      '?project=AGB&limit=20&label=api&label=cli',
    )
  })
  it('값이 없으면 빈 문자열이다', () => {
    expect(buildQuery({})).toBe('')
    expect(buildQuery({ a: undefined, b: null, c: '' })).toBe('')
  })
  it('한글과 공백을 인코딩한다(공백은 +)', () => {
    expect(buildQuery({ q: '설계 api' })).toBe('?q=%EC%84%A4%EA%B3%84+api')
  })
})

describe('issueListQuery', () => {
  it('상태·종류·우선순위는 쉼표 OR, 라벨은 반복 AND로 만든다', () => {
    expect(
      issueListQuery({
        project: 'AGB',
        status: ['todo', 'doing'],
        type: ['feature'],
        priority: ['P0', 'P1'],
        label: ['api', 'cli'],
        q: '',
        sort: '-updated_at',
        limit: 100,
      }),
    ).toBe('?project=AGB&status=todo%2Cdoing&type=feature&priority=P0%2CP1&label=api&label=cli&sort=-updated_at&limit=100')
  })
  it('주제 필터는 쉼표 OR로 만들고 비면 생략한다', () => {
    const base = { project: 'AGB', status: [], type: [], priority: [], label: [], q: '', sort: '' }
    expect(issueListQuery({ ...base, topic: ['4', 'none'] })).toBe('?project=AGB&topic=4%2Cnone')
    expect(issueListQuery({ ...base, topic: [] })).toBe('?project=AGB')
  })
  it('마일스톤 필터는 쉼표 OR로 만들고 비면 생략한다', () => {
    const base = { project: 'AGB', status: [], type: [], priority: [], label: [], q: '', sort: '' }
    expect(issueListQuery({ ...base, milestone: ['2'] })).toBe('?project=AGB&milestone=2')
    expect(issueListQuery({ ...base, milestone: ['2', 'none'] })).toBe('?project=AGB&milestone=2%2Cnone')
    expect(issueListQuery({ ...base, milestone: ['none'] })).toBe('?project=AGB&milestone=none')
    expect(issueListQuery({ ...base, milestone: [] })).toBe('?project=AGB')
    expect(issueListQuery(base)).toBe('?project=AGB')
  })
  it('주제와 마일스톤 필터를 함께 쓰면 두 매개변수를 모두 붙인다', () => {
    const base = { project: 'AGB', status: [], type: [], priority: [], label: [], q: '', sort: '' }
    expect(issueListQuery({ ...base, topic: ['4'], milestone: ['2'] })).toBe('?project=AGB&topic=4&milestone=2')
  })
  it('확인 여부 필터는 값이 있을 때만 붙인다', () => {
    const base = { project: 'AGB', status: [], type: [], priority: [], label: [], q: '', sort: '' }
    expect(issueListQuery({ ...base, acknowledged: 'false' })).toBe('?project=AGB&acknowledged=false')
    expect(issueListQuery({ ...base, acknowledged: 'true' })).toBe('?project=AGB&acknowledged=true')
    expect(issueListQuery(base)).toBe('?project=AGB')
  })
  it('빈 배열과 전체 프로젝트는 생략한다', () => {
    expect(issueListQuery({ project: null, status: [], type: [], priority: [], label: [], q: '', sort: '' })).toBe('')
  })
})

describe('headersFor', () => {
  it('Bearer 토큰과 추적 헤더 세 개를 만든다', () => {
    expect(headersFor(config)).toEqual({
      Authorization: `Bearer ${config.token}`,
      'X-Actor': 'owner',
      'X-Session-Id': 'web-0123456789abcdef',
      'X-Client-Host': 'web',
    })
  })
})

describe('ApiClient', () => {
  it('GET은 baseUrl + /v1 + 경로 + 질의로 부르고 헤더를 붙인다', async () => {
    const { calls, fetchFn } = fakeFetch(200, { items: [], total: 0 })
    const client = new ApiClient(config, fetchFn)
    const data = await client.get<{ items: unknown[] }>('/issues', { project: 'AGB', status: 'open' })
    expect(data.items).toEqual([])
    expect(calls[0].url).toBe('/api/staging/v1/issues?project=AGB&status=open')
    expect(calls[0].init.method).toBe('GET')
    const headers = calls[0].init.headers as Record<string, string>
    expect(headers.Authorization).toBe(`Bearer ${config.token}`)
    expect(headers['X-Actor']).toBe('owner')
    expect(headers['X-Session-Id']).toBe('web-0123456789abcdef')
    expect(headers['X-Client-Host']).toBe('web')
    expect(headers['Content-Type']).toBeUndefined()
  })

  it('baseUrl이 빈 문자열이면 같은 출처의 /v1을 부른다', async () => {
    const { calls, fetchFn } = fakeFetch(200, { status: 'ok' })
    const client = new ApiClient({ ...config, baseUrl: '' }, fetchFn)
    await client.get('/board')
    expect(calls[0].url).toBe('/v1/board')
  })

  it('PATCH는 JSON 본문과 If-Match 헤더를 보낸다', async () => {
    const { calls, fetchFn } = fakeFetch(200, { key: 'AGB-1', version: 8 })
    const client = new ApiClient(config, fetchFn)
    await client.patch('/issues/AGB-1', { status: 'doing' }, { ifMatch: 7 })
    expect(calls[0].init.method).toBe('PATCH')
    expect(calls[0].init.body).toBe('{"status":"doing"}')
    const headers = calls[0].init.headers as Record<string, string>
    expect(headers['If-Match']).toBe('"7"')
    expect(headers['Content-Type']).toBe('application/json; charset=utf-8')
  })

  it('POST는 201 응답 본문을 돌려준다', async () => {
    const { calls, fetchFn } = fakeFetch(201, { key: 'AGB-2' })
    const client = new ApiClient(config, fetchFn)
    const created = await client.post<{ key: string }>('/issues', { project: 'AGB', title: 't' })
    expect(created.key).toBe('AGB-2')
    expect(calls[0].init.method).toBe('POST')
  })

  it('DELETE는 If-Match를 보낼 수 있다', async () => {
    const { calls, fetchFn } = fakeFetch(200, { key: 'AGB-1', status: 'deleted' })
    const client = new ApiClient(config, fetchFn)
    await client.delete('/issues/AGB-1', { ifMatch: 3 })
    expect(calls[0].init.method).toBe('DELETE')
    expect((calls[0].init.headers as Record<string, string>)['If-Match']).toBe('"3"')
  })

  it('오류 응답은 code, message, details를 가진 ApiError다', async () => {
    const { fetchFn } = fakeFetch(422, {
      error: {
        code: 'validation_failed',
        message: '요청 값이 규칙에 맞지 않습니다.',
        details: [{ field: 'priority', message: 'P0, P1, P2, P3 중 하나여야 합니다.' }],
      },
    })
    const client = new ApiClient(config, fetchFn)
    const err = await client.post('/issues', {}).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    const apiErr = err as ApiError
    expect(apiErr.status).toBe(422)
    expect(apiErr.code).toBe('validation_failed')
    expect(apiErr.message).toBe('요청 값이 규칙에 맞지 않습니다.')
    expect(apiErr.details).toEqual([{ field: 'priority', message: 'P0, P1, P2, P3 중 하나여야 합니다.' }])
    expect(apiErr.detailFor('priority')).toBe('P0, P1, P2, P3 중 하나여야 합니다.')
    expect(apiErr.detailFor('title')).toBeNull()
  })

  it('412는 precondition_failed로 구분한다', async () => {
    const { fetchFn } = fakeFetch(412, {
      error: { code: 'precondition_failed', message: 'If-Match의 버전이 현재 버전과 다릅니다.' },
    })
    const client = new ApiClient(config, fetchFn)
    const err = await client.patch('/issues/AGB-1', { status: 'done' }, { ifMatch: 1 }).catch((e: unknown) => e)
    expect(isPreconditionFailed(err)).toBe(true)
    expect(isUnauthorized(err)).toBe(false)
    expect((err as ApiError).details).toEqual([])
  })

  it('401은 unauthorized로 구분한다', async () => {
    const { fetchFn } = fakeFetch(401, { error: { code: 'unauthorized', message: '유효한 Bearer 토큰이 필요합니다.' } })
    const client = new ApiClient(config, fetchFn)
    const err = await client.get('/projects').catch((e: unknown) => e)
    expect(isUnauthorized(err)).toBe(true)
    expect(isPreconditionFailed(err)).toBe(false)
  })

  it('fetch 자체가 실패하면 NetworkError다', async () => {
    const client = new ApiClient(config, async () => {
      throw new TypeError('Failed to fetch')
    })
    const err = await client.get('/projects').catch((e: unknown) => e)
    expect(err).toBeInstanceOf(NetworkError)
  })

  it('502와 JSON이 아닌 응답은 ServerUnavailableError다', async () => {
    const gateway = fakeFetch(502, { error: { code: 'upstream_unavailable', message: '연결할 수 없습니다.' } })
    const err1 = await new ApiClient(config, gateway.fetchFn).get('/projects').catch((e: unknown) => e)
    expect(err1).toBeInstanceOf(ServerUnavailableError)
    expect((err1 as ServerUnavailableError).status).toBe(502)

    const html = fakeFetch(500, '<html>Internal Server Error</html>', { 'content-type': 'text/html' })
    const err2 = await new ApiClient(config, html.fetchFn).get('/projects').catch((e: unknown) => e)
    expect(err2).toBeInstanceOf(ServerUnavailableError)
  })

  it('AbortSignal을 fetch에 넘긴다', async () => {
    const { calls, fetchFn } = fakeFetch(200, {})
    const client = new ApiClient(config, fetchFn)
    const ctrl = new AbortController()
    await client.get('/board', undefined, ctrl.signal)
    expect(calls[0].init.signal).toBe(ctrl.signal)
  })
})
