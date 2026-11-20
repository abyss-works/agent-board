import {
  isAbortError,
  isActorRejected,
  isUnauthorized,
  ServerUnavailableError,
  type ApiClient,
  type EventStreamParams,
} from './client'
import { QUERY_ROOT } from './queries'
import type { EventItem } from './types'

export interface EventStreamHandlers {
  onEvent: (ev: EventItem) => void
  onHeartbeat?: () => void
  onMalformed?: (raw: string, err: unknown) => void
  onId?: (id: string) => void
}

export interface EventStreamCallbacks {
  onOpen?: (info: { reconnected: boolean; hadCursor: boolean }) => void
  onEvent: (ev: EventItem) => void
  onHeartbeat?: () => void
  onMalformed?: (raw: string, err: unknown) => void
  onDisconnect?: (err?: unknown) => void
  onAuthError?: (err: unknown) => void
}

export interface SubscribeEventStreamOptions {
  signal?: AbortSignal
  project?: string
  kind?: string
  after?: number
  retryDelayMs?: (attempt: number) => number
  sleep?: (ms: number, signal?: AbortSignal) => Promise<void>
}

const MAX_BACKOFF_MS = 15_000

function defaultRetryDelayMs(attempt: number): number {
  return Math.min(1000 * 2 ** attempt, MAX_BACKOFF_MS)
}

function defaultSleep(ms: number, signal?: AbortSignal): Promise<void> {
  if (signal?.aborted) return Promise.resolve()
  return new Promise((resolve) => {
    const onAbort = () => {
      clearTimeout(timer)
      resolve()
    }
    const timer = setTimeout(() => {
      signal?.removeEventListener('abort', onAbort)
      resolve()
    }, ms)
    signal?.addEventListener('abort', onAbort, { once: true })
  })
}

function takeLine(buffer: string, flush: boolean): { line: string; rest: string } | null {
  for (let i = 0; i < buffer.length; i++) {
    const ch = buffer[i]
    if (ch === '\n') {
      return { line: buffer.slice(0, i), rest: buffer.slice(i + 1) }
    }
    if (ch === '\r') {
      if (i + 1 < buffer.length) {
        const next = buffer[i + 1]
        return { line: buffer.slice(0, i), rest: buffer.slice(next === '\n' ? i + 2 : i + 1) }
      }
      if (flush) {
        return { line: buffer.slice(0, i), rest: '' }
      }
      return null
    }
  }
  return null
}

async function readWithAbort(
  reader: ReadableStreamDefaultReader<Uint8Array>,
  signal?: AbortSignal,
): Promise<ReadableStreamReadResult<Uint8Array>> {
  if (!signal) return reader.read()
  if (signal.aborted) return { value: undefined, done: true }
  return new Promise<ReadableStreamReadResult<Uint8Array>>((resolve, reject) => {
    const onAbort = () => {
      void reader.cancel?.().catch(() => {})
      resolve({ value: undefined, done: true })
    }
    signal.addEventListener('abort', onAbort, { once: true })
    reader.read().then(
      (res) => {
        signal.removeEventListener('abort', onAbort)
        resolve(res)
      },
      (err) => {
        signal.removeEventListener('abort', onAbort)
        reject(err)
      },
    )
  })
}

/** 증분 SSE 스트림을 읽어 이벤트를 파싱한다. UTF-8·줄 경계 분할과 잘못된 JSON을 견딘다. */
export async function readEventStream(
  body: ReadableStream<Uint8Array>,
  handlers: EventStreamHandlers,
  signal?: AbortSignal,
): Promise<void> {
  if (signal?.aborted) {
    await body.cancel().catch(() => {})
    return
  }

  const reader = body.getReader()
  const decoder = new TextDecoder('utf-8')
  const state = {
    eventType: '',
    eventId: undefined as string | undefined,
    dataLines: [] as string[],
  }
  let buffer = ''

  const dispatch = () => {
    const rawData = state.dataLines.join('\n')
    const dispatchId = state.eventId
    state.dataLines = []
    state.eventType = ''
    state.eventId = undefined
    if (!rawData) return

    let parsed: unknown
    try {
      parsed = JSON.parse(rawData)
    } catch (err) {
      handlers.onMalformed?.(rawData, err)
      return
    }
    if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
      handlers.onMalformed?.(rawData, new Error('이벤트 페이로드가 JSON 객체가 아닙니다.'))
      return
    }
    if (dispatchId !== undefined) handlers.onId?.(dispatchId)
    handlers.onEvent(parsed as EventItem)
  }

  const processLine = (line: string) => {
    if (line === '') {
      if (state.dataLines.length === 0) {
        state.eventType = ''
        state.eventId = undefined
        return
      }
      dispatch()
      return
    }
    if (line.startsWith(':')) {
      handlers.onHeartbeat?.()
      return
    }
    const colonIdx = line.indexOf(':')
    const field = colonIdx === -1 ? line : line.slice(0, colonIdx)
    let value = colonIdx === -1 ? '' : line.slice(colonIdx + 1)
    if (value.startsWith(' ')) value = value.slice(1)

    if (field === 'data') state.dataLines.push(value)
    else if (field === 'event') state.eventType = value
    else if (field === 'id' && !value.includes('\0')) state.eventId = value
  }

  try {
    while (!signal?.aborted) {
      const { value, done } = await readWithAbort(reader, signal)
      if (signal?.aborted) return
      if (done) break
      if (value && value.byteLength > 0) {
        buffer += decoder.decode(value, { stream: true })
        while (!signal?.aborted) {
          const taken = takeLine(buffer, false)
          if (!taken) break
          buffer = taken.rest
          processLine(taken.line)
        }
      }
    }
    if (signal?.aborted) return
    buffer += decoder.decode()
    while (!signal?.aborted) {
      const taken = takeLine(buffer, true)
      if (!taken) break
      buffer = taken.rest
      processLine(taken.line)
    }
  } catch (err) {
    if (signal?.aborted || isAbortError(err)) return
    throw err
  } finally {
    reader.releaseLock()
  }
}

/** 이벤트 종류에 따라 무효화해야 할 React Query 키 목록을 돌려준다. */
export function queryKeysForEvent(ev: EventItem): readonly (readonly unknown[])[] {
  const dot = ev.kind.indexOf('.')
  if (dot === -1) return []
  const prefix = ev.kind.slice(0, dot)
  switch (prefix) {
    case 'issue':
      return [
        [QUERY_ROOT, 'issues'],
        [QUERY_ROOT, 'issue', ev.subject],
        [QUERY_ROOT, 'issue-activity', ev.subject],
        [QUERY_ROOT, 'activity'],
        [QUERY_ROOT, 'board'],
        [QUERY_ROOT, 'queue'],
        [QUERY_ROOT, 'overview'],
        [QUERY_ROOT, 'agenda'],
        [QUERY_ROOT, 'labels'],
        [QUERY_ROOT, 'milestones'],
        [QUERY_ROOT, 'sessions'],
        [QUERY_ROOT, 'session'],
      ]
    case 'project':
      return [
        [QUERY_ROOT, 'projects'],
        [QUERY_ROOT, 'overview'],
        [QUERY_ROOT, 'agenda'],
        [QUERY_ROOT, 'board'],
      ]
    case 'topic':
      return [
        [QUERY_ROOT, 'topics'],
        [QUERY_ROOT, 'issues'],
        [QUERY_ROOT, 'issue'],
        [QUERY_ROOT, 'overview'],
        [QUERY_ROOT, 'board'],
      ]
    case 'milestone':
      return [
        [QUERY_ROOT, 'milestones'],
        [QUERY_ROOT, 'issues'],
        [QUERY_ROOT, 'issue'],
        [QUERY_ROOT, 'overview'],
        [QUERY_ROOT, 'board'],
        [QUERY_ROOT, 'queue'],
      ]
    case 'document':
      return [
        [QUERY_ROOT, 'documents'],
        [QUERY_ROOT, 'document'],
        [QUERY_ROOT, 'overview'],
        [QUERY_ROOT, 'sessions'],
        [QUERY_ROOT, 'session'],
      ]
    case 'session':
      return [
        [QUERY_ROOT, 'sessions'],
        [QUERY_ROOT, 'session'],
        [QUERY_ROOT, 'agenda'],
        [QUERY_ROOT, 'overview'],
      ]
    case 'claim':
      return [
        [QUERY_ROOT, 'overview'],
        [QUERY_ROOT, 'agenda'],
        [QUERY_ROOT, 'board'],
        [QUERY_ROOT, 'sessions'],
        [QUERY_ROOT, 'session'],
        [QUERY_ROOT, 'issue', ev.subject],
        [QUERY_ROOT, 'issues'],
      ]
    default:
      return []
  }
}

/**
 * `/v1/events/stream`을 끊기면 `Last-Event-ID`와 지수 백오프로 재연결하며 구독한다.
 * 401 또는 행위자 거절 오류는 재연결 없이 즉시 종료한다.
 */
export async function subscribeEventStream(
  client: Pick<ApiClient, 'openEventStream'>,
  callbacks: EventStreamCallbacks,
  opts: SubscribeEventStreamOptions = {},
): Promise<void> {
  const { signal, retryDelayMs = defaultRetryDelayMs, sleep = defaultSleep } = opts
  let lastEventId: string | undefined
  let hadDisconnect = false
  let attempt = 0

  while (!signal?.aborted) {
    let disconnectErr: unknown
    try {
      const params: EventStreamParams = { lastEventId }
      if (opts.project !== undefined) params.project = opts.project
      if (opts.kind !== undefined) params.kind = opts.kind
      if (opts.after !== undefined) params.after = opts.after

      const res = await client.openEventStream(params, signal)
      if (signal?.aborted) {
        await res.body?.cancel().catch(() => {})
        return
      }
      if (!res.body) throw new ServerUnavailableError(res.status)

      attempt = 0
      callbacks.onOpen?.({ reconnected: hadDisconnect, hadCursor: lastEventId !== undefined })
      if (signal?.aborted) {
        await res.body.cancel().catch(() => {})
        return
      }

      let currentEventId: string | undefined
      await readEventStream(
        res.body,
        {
          onId: (id) => {
            currentEventId = id || undefined
          },
          onEvent: (ev) => {
            lastEventId = currentEventId ?? String(ev.id)
            currentEventId = undefined
            callbacks.onEvent(ev)
          },
          onHeartbeat: () => callbacks.onHeartbeat?.(),
          onMalformed: (raw, err) => callbacks.onMalformed?.(raw, err),
        },
        signal,
      )

      if (signal?.aborted) return
    } catch (err) {
      if (signal?.aborted || isAbortError(err)) return
      if (isUnauthorized(err) || isActorRejected(err)) {
        callbacks.onAuthError?.(err)
        return
      }
      disconnectErr = err
    }

    if (signal?.aborted) return
    hadDisconnect = true
    callbacks.onDisconnect?.(disconnectErr)
    if (signal?.aborted) return

    const delay = retryDelayMs(attempt)
    attempt += 1
    try {
      await sleep(delay, signal)
    } catch (err) {
      if (signal?.aborted || isAbortError(err)) return
      throw err
    }
  }
}
