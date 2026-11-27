import { describe, expect, it, vi } from 'vitest'
import { ApiClient, ApiError, NetworkError, ServerUnavailableError, type ClientConfig } from './client'
import { queryKeysForEvent, readEventStream, subscribeEventStream } from './events'
import type { EventItem } from './types'

const encoder = new TextEncoder()

function eventFixture(partial: Partial<EventItem> = {}): EventItem {
  return {
    id: 1,
    at: '2026-10-17T07:00:00.000Z',
    project: 'APP',
    kind: 'issue.created',
    subject: 'APP-1',
    actor: 'agent-a',
    session_id: 'sess-1',
    data: { fields: ['title'] },
    ...partial,
  }
}

function streamFromChunks(chunks: (string | Uint8Array)[]): ReadableStream<Uint8Array> {
  return new ReadableStream<Uint8Array>({
    start(controller) {
      for (const chunk of chunks) {
        controller.enqueue(typeof chunk === 'string' ? encoder.encode(chunk) : chunk)
      }
      controller.close()
    },
  })
}

const config: ClientConfig = {
  baseUrl: '/api',
  token: 'test-token',
  actor: 'owner',
  sessionId: 'web-0123456789abcdef',
  clientHost: 'web',
}

describe('readEventStream', () => {
  it('단건과 다건 SSE 이벤트를 파싱해 순서대로 전달한다', async () => {
    const ev1 = eventFixture({ id: 1, kind: 'issue.created', subject: 'APP-1' })
    const ev2 = eventFixture({ id: 2, kind: 'issue.updated', subject: 'APP-2' })
    const body = streamFromChunks([
      `id: 1\nevent: issue.created\ndata: ${JSON.stringify(ev1)}\n\n` +
        `id: 2\nevent: issue.updated\ndata: ${JSON.stringify(ev2)}\n\n`,
    ])

    const received: EventItem[] = []
    await readEventStream(body, { onEvent: (ev) => received.push(ev) })
    expect(received).toEqual([ev1, ev2])
  })

  it('UTF-8 멀티바이트와 줄 경계가 임의의 청크로 쪼개져도 정확히 복원한다', async () => {
    const ev = eventFixture({
      id: 10,
      kind: 'issue.commented',
      subject: 'APP-10',
      data: { summary: '한글 멀티바이트 테스트 🎉' },
    })
    const payload = encoder.encode(`id: 10\r\nevent: issue.commented\r\ndata: ${JSON.stringify(ev)}\r\n\r\n`)

    // 1~3바이트 단위로 쪼개어 UTF-8 경계와 \r\n 경계를 모두 분할한다.
    const chunks: Uint8Array[] = []
    for (let i = 0; i < payload.length; ) {
      const size = (i % 3) + 1
      chunks.push(payload.slice(i, i + size))
      i += size
    }

    const received: EventItem[] = []
    await readEventStream(streamFromChunks(chunks), { onEvent: (item) => received.push(item) })
    expect(received).toEqual([ev])
  })

  it('CRLF 줄바꿈과 여러 줄 data 필드를 이어 붙여 파싱한다', async () => {
    const ev = eventFixture({ id: 3, kind: 'project.updated', subject: 'APP' })
    const jsonLines = JSON.stringify(ev, null, 2)
      .split('\n')
      .map((line) => `data: ${line}`)
      .join('\r\n')
    const body = streamFromChunks([`id: 3\r\nevent: project.updated\r\n${jsonLines}\r\n\r\n`])

    const received: EventItem[] = []
    await readEventStream(body, { onEvent: (item) => received.push(item) })
    expect(received).toEqual([ev])
  })

  it('주석 줄(: ping 등)은 heartbeat 콜백만 부르고 이벤트 파싱을 깨지 않는다', async () => {
    const ev = eventFixture({ id: 4, kind: 'session.updated', subject: 'agent-a/s1' })
    const body = streamFromChunks([
      ': ping\n\n',
      `id: 4\n: keepalive\nevent: session.updated\ndata: ${JSON.stringify(ev)}\n\n`,
    ])

    const received: EventItem[] = []
    const onHeartbeat = vi.fn()
    await readEventStream(body, { onEvent: (item) => received.push(item), onHeartbeat })
    expect(onHeartbeat).toHaveBeenCalledTimes(2)
    expect(received).toEqual([ev])
  })

  it('잘못된 JSON이 와도 스트림을 죽이지 않고 onMalformed를 부른 뒤 다음 이벤트를 읽는다', async () => {
    const valid = eventFixture({ id: 6, kind: 'claim.acquired', subject: 'APP-6' })
    const body = streamFromChunks([
      'id: 5\nevent: issue.updated\ndata: {broken-json\n\n',
      `id: 6\nevent: claim.acquired\ndata: ${JSON.stringify(valid)}\n\n`,
    ])

    const received: EventItem[] = []
    const malformed: Array<{ raw: string; err: unknown }> = []
    await readEventStream(body, {
      onEvent: (item) => received.push(item),
      onMalformed: (raw, err) => malformed.push({ raw, err }),
    })
    expect(malformed).toHaveLength(1)
    expect(malformed[0].raw).toBe('{broken-json')
    expect(malformed[0].err).toBeInstanceOf(Error)
    expect(received).toEqual([valid])
  })

  it('AbortSignal이 중단되면 열린 스트림도 깔끔히 종료한다', async () => {
    const hanging = new ReadableStream<Uint8Array>({
      start() {
        // 닫히지 않는 스트림
      },
    })
    const ctrl = new AbortController()
    const promise = readEventStream(hanging, { onEvent: vi.fn() }, ctrl.signal)
    ctrl.abort()
    await expect(promise).resolves.toBeUndefined()
  })
})

describe('queryKeysForEvent', () => {
  it('issue.* 이벤트의 무효화 키 목록을 돌려준다', () => {
    const keys = queryKeysForEvent(eventFixture({ kind: 'issue.updated', subject: 'AGB-42' }))
    expect(keys).toEqual([
      ['agb', 'issues'],
      ['agb', 'issue', 'AGB-42'],
      ['agb', 'issue-activity', 'AGB-42'],
      ['agb', 'activity'],
      ['agb', 'board'],
      ['agb', 'queue'],
      ['agb', 'overview'],
      ['agb', 'agenda'],
      ['agb', 'labels'],
      ['agb', 'milestones'],
      ['agb', 'sessions'],
      ['agb', 'session'],
    ])
  })

  it('project.* 이벤트의 무효화 키 목록을 돌려준다', () => {
    expect(queryKeysForEvent(eventFixture({ kind: 'project.created', subject: 'APP' }))).toEqual([
      ['agb', 'projects'],
      ['agb', 'overview'],
      ['agb', 'agenda'],
      ['agb', 'board'],
      ['agb', 'sessions'],
      ['agb', 'session'],
    ])
  })

  it('topic.* 이벤트의 무효화 키 목록을 돌려준다', () => {
    expect(queryKeysForEvent(eventFixture({ kind: 'topic.updated', subject: '12' }))).toEqual([
      ['agb', 'topics'],
      ['agb', 'issues'],
      ['agb', 'issue'],
      ['agb', 'overview'],
      ['agb', 'board'],
      ['agb', 'agenda'],
      ['agb', 'queue'],
      ['agb', 'sessions'],
      ['agb', 'session'],
    ])
  })

  it('milestone.* 이벤트의 무효화 키 목록을 돌려준다', () => {
    expect(queryKeysForEvent(eventFixture({ kind: 'milestone.created', subject: '3' }))).toEqual([
      ['agb', 'milestones'],
      ['agb', 'issues'],
      ['agb', 'issue'],
      ['agb', 'overview'],
      ['agb', 'board'],
      ['agb', 'queue'],
      ['agb', 'agenda'],
      ['agb', 'sessions'],
      ['agb', 'session'],
    ])
  })

  it('document.* 이벤트의 무효화 키 목록을 돌려준다', () => {
    expect(queryKeysForEvent(eventFixture({ kind: 'document.updated', subject: '2026-10-17-note.md' }))).toEqual([
      ['agb', 'documents'],
      ['agb', 'document'],
      ['agb', 'overview'],
      ['agb', 'sessions'],
      ['agb', 'session'],
    ])
  })

  it('session.* 이벤트의 무효화 키 목록을 돌려준다', () => {
    expect(queryKeysForEvent(eventFixture({ kind: 'session.created', subject: 'agent-a/s1' }))).toEqual([
      ['agb', 'sessions'],
      ['agb', 'session'],
      ['agb', 'agenda'],
      ['agb', 'overview'],
    ])
  })

  it('claim.* 이벤트의 무효화 키 목록을 돌려준다', () => {
    expect(queryKeysForEvent(eventFixture({ kind: 'claim.released', subject: 'AGB-99' }))).toEqual([
      ['agb', 'overview'],
      ['agb', 'agenda'],
      ['agb', 'board'],
      ['agb', 'sessions'],
      ['agb', 'session'],
      ['agb', 'issue', 'AGB-99'],
      ['agb', 'issues'],
    ])
  })

  it('message.* 및 알 수 없는 접두사는 빈 배열을 돌려준다', () => {
    expect(queryKeysForEvent(eventFixture({ kind: 'message.created', subject: '1' }))).toEqual([])
    expect(queryKeysForEvent(eventFixture({ kind: 'unknown.event', subject: 'x' }))).toEqual([])
    expect(queryKeysForEvent(eventFixture({ kind: 'issue', subject: 'AGB-1' }))).toEqual([])
  })
})

describe('subscribeEventStream', () => {
  it('재연결 시 직전 이벤트 ID를 Last-Event-ID로 보내고 연결 상태를 알린다', async () => {
    const ev1 = eventFixture({ id: 11, kind: 'issue.created', subject: 'APP-11' })
    const ev2 = eventFixture({ id: 12, kind: 'issue.updated', subject: 'APP-11' })
    const calls: Array<{ url: string; headers: Record<string, string> }> = []
    const ctrl = new AbortController()
    let count = 0

    const client = new ApiClient(config, async (input, init) => {
      calls.push({ url: String(input), headers: (init?.headers ?? {}) as Record<string, string> })
      count += 1
      if (count === 1) {
        return new Response(
          streamFromChunks([': ping\n\n', `id: 11\nevent: issue.created\ndata: ${JSON.stringify(ev1)}\n\n`]),
          { status: 200, headers: { 'content-type': 'text/event-stream' } },
        )
      }
      return new Response(
        streamFromChunks([`id: 12\nevent: issue.updated\ndata: ${JSON.stringify(ev2)}\n\n`]),
        { status: 200, headers: { 'content-type': 'text/event-stream' } },
      )
    })

    const opened: Array<{ reconnected: boolean; hadCursor: boolean }> = []
    const events: EventItem[] = []
    const onHeartbeat = vi.fn()
    const onDisconnect = vi.fn()
    const onAuthError = vi.fn()

    await subscribeEventStream(
      client,
      {
        onOpen: (info) => opened.push(info),
        onEvent: (ev) => {
          events.push(ev)
          if (ev.id === 12) ctrl.abort()
        },
        onHeartbeat,
        onDisconnect,
        onAuthError,
      },
      { signal: ctrl.signal, sleep: async () => {} },
    )

    expect(opened).toEqual([
      { reconnected: false, hadCursor: false },
      { reconnected: true, hadCursor: true },
    ])
    expect(events).toEqual([ev1, ev2])
    expect(onHeartbeat).toHaveBeenCalledTimes(1)
    expect(onDisconnect).toHaveBeenCalledTimes(1)
    expect(onAuthError).not.toHaveBeenCalled()
    expect(calls[0].headers['Last-Event-ID']).toBeUndefined()
    expect(calls[1].headers['Last-Event-ID']).toBe('11')
  })

  it('일시 오류 시 지수 백오프로 재시도하고 연결이 열리면 백오프를 초기화한다', async () => {
    const ctrl = new AbortController()
    const sleeps: number[] = []
    let callCount = 0

    const client = new ApiClient(config, async () => {
      callCount += 1
      if (callCount <= 6) {
        if (callCount % 2 === 0) throw new TypeError('network down')
        return new Response('unavailable', { status: 503, headers: { 'content-type': 'text/plain' } })
      }
      if (callCount === 7) {
        // 한 번 성공해서 백오프를 초기화한 뒤 EOF로 끊긴다.
        return new Response(streamFromChunks([]), { status: 200, headers: { 'content-type': 'text/event-stream' } })
      }
      throw new TypeError('network down again')
    })

    const onDisconnect = vi.fn()
    const onOpen = vi.fn()

    await subscribeEventStream(
      client,
      {
        onOpen,
        onEvent: vi.fn(),
        onDisconnect,
        onAuthError: vi.fn(),
      },
      {
        signal: ctrl.signal,
        sleep: async (ms) => {
          sleeps.push(ms)
          if (sleeps.length >= 8) ctrl.abort()
        },
      },
    )

    expect(sleeps).toEqual([1000, 2000, 4000, 8000, 15000, 15000, 1000, 2000])
    expect(onOpen).toHaveBeenCalledWith({ reconnected: true, hadCursor: false })
    expect(onDisconnect).toHaveBeenCalledTimes(8)
    expect(onDisconnect.mock.calls[0][0]).toBeInstanceOf(ServerUnavailableError)
    expect(onDisconnect.mock.calls[1][0]).toBeInstanceOf(NetworkError)
  })

  it.each([
    [
      '401 unauthorized',
      () => new Response(JSON.stringify({ error: { code: 'unauthorized', message: '만료' } }), { status: 401, headers: { 'content-type': 'application/json' } }),
    ],
    [
      '403 actor_mismatch',
      () =>
        new Response(
          JSON.stringify({ error: { code: 'forbidden', message: '불일치', details: [{ field: 'X-Actor', reason: 'actor_mismatch' }] } }),
          { status: 403, headers: { 'content-type': 'application/json' } },
        ),
    ],
  ])('%s 오류 시 재연결 없이 즉시 onAuthError를 부르고 종료한다', async (_label, makeResponse) => {
    const fetchFn = vi.fn(async () => makeResponse())
    const client = new ApiClient(config, fetchFn)
    const onAuthError = vi.fn()
    const onDisconnect = vi.fn()
    const sleep = vi.fn(async () => {})

    await subscribeEventStream(
      client,
      { onEvent: vi.fn(), onAuthError, onDisconnect },
      { sleep },
    )

    expect(fetchFn).toHaveBeenCalledTimes(1)
    expect(onAuthError).toHaveBeenCalledTimes(1)
    expect(onAuthError.mock.calls[0][0]).toBeInstanceOf(ApiError)
    expect(onDisconnect).not.toHaveBeenCalled()
    expect(sleep).not.toHaveBeenCalled()
  })

  it('AbortSignal이 중단되면 콜백 없이 깔끔히 종료한다', async () => {
    const ctrl = new AbortController()
    const client = new ApiClient(config, async (_input, init) => {
      expect(init?.signal).toBe(ctrl.signal)
      ctrl.abort()
      throw new DOMException('Aborted', 'AbortError')
    })

    const onAuthError = vi.fn()
    const onDisconnect = vi.fn()
    const onOpen = vi.fn()
    await expect(
      subscribeEventStream(
        client,
        { onOpen, onEvent: vi.fn(), onAuthError, onDisconnect },
        { signal: ctrl.signal },
      ),
    ).resolves.toBeUndefined()

    expect(onOpen).not.toHaveBeenCalled()
    expect(onAuthError).not.toHaveBeenCalled()
    expect(onDisconnect).not.toHaveBeenCalled()
  })

  it('커서 없이 끊겨 재연결하면 hadCursor가 false이고, SSE id 줄이 없어도 ev.id로 커서를 갱신한다', async () => {
    const ev = eventFixture({ id: 77, kind: 'issue.updated', subject: 'APP-77' })
    const headersList: Array<Record<string, string>> = []
    const ctrl = new AbortController()
    let attempt = 0

    const client = new ApiClient(config, async (_input, init) => {
      headersList.push((init?.headers ?? {}) as Record<string, string>)
      attempt += 1
      if (attempt === 1) {
        return new Response(streamFromChunks([]), { status: 200, headers: { 'content-type': 'text/event-stream' } })
      }
      if (attempt === 2) {
        return new Response(streamFromChunks([`data: ${JSON.stringify(ev)}\n\n`]), {
          status: 200,
          headers: { 'content-type': 'text/event-stream' },
        })
      }
      ctrl.abort()
      throw new DOMException('Aborted', 'AbortError')
    })

    const opened: Array<{ reconnected: boolean; hadCursor: boolean }> = []
    await subscribeEventStream(
      client,
      {
        onOpen: (info) => opened.push(info),
        onEvent: vi.fn(),
        onAuthError: vi.fn(),
      },
      { signal: ctrl.signal, sleep: async () => {} },
    )

    expect(opened).toEqual([
      { reconnected: false, hadCursor: false },
      { reconnected: true, hadCursor: false },
    ])
    expect(headersList[0]['Last-Event-ID']).toBeUndefined()
    expect(headersList[1]['Last-Event-ID']).toBeUndefined()
    expect(headersList[2]['Last-Event-ID']).toBe('77')
  })
})
