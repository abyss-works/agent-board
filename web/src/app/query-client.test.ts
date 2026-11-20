import { environmentManager, QueryObserver } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, NetworkError, ServerUnavailableError } from '@/api/client'
import { AppStorage, memoryStore, type KeyValueStore } from '@/lib/storage'
import { connection } from './connection'
import { createQueryClient, LOGIN_ACTORS_KEY, refreshForActor } from './query-client'
import { FALLBACK_POLL_MS } from './state'

const rejected = (status: number, reason: string) => new ApiError(status, 'error', '거절', [{ reason }])
const LIST_KEY = [...LOGIN_ACTORS_KEY, 'token']

function setup(store: KeyValueStore = memoryStore()) {
  const storage = new AppStorage(store)
  storage.setActor('gone')
  const onUnauthorized = vi.fn()
  const onActorCleared = vi.fn()
  const client = createQueryClient({ storage, onUnauthorized, onActorCleared })
  client.setQueryData(LIST_KEY, ['me', 'boss'])
  const fail = (err: unknown) => client.fetchQuery({ queryKey: ['agb', 'x'], queryFn: () => Promise.reject(err), retry: false }).catch(() => {})
  return { storage, client, onUnauthorized, onActorCleared, fail }
}

describe('createQueryClient 행위자 거절', () => {
  it.each([
    [400, 'unknown_actor'],
    [403, 'actor_mismatch'],
  ])('%i %s이면 목록 캐시를 버리고 저장된 행위자를 지운 뒤 알린다', async (status, reason) => {
    const { storage, client, onUnauthorized, onActorCleared, fail } = setup()
    await fail(rejected(status, reason))
    expect(client.getQueryData(LIST_KEY)).toBeUndefined()
    expect(storage.getActor()).toBeNull()
    expect(onActorCleared).toHaveBeenCalledTimes(1)
    expect(onUnauthorized).not.toHaveBeenCalled()
  })

  it('저장된 행위자를 지우는 시점에 오래된 목록 캐시는 이미 없다', async () => {
    const mem = memoryStore()
    let cacheAtClear: unknown = 'unset'
    let client: ReturnType<typeof setup>['client'] | null = null
    const store: KeyValueStore = {
      ...mem,
      removeItem: (k) => {
        if (k === 'agb.actor') cacheAtClear = client?.getQueryData(LIST_KEY)
        mem.removeItem(k)
      },
    }
    const s = setup(store)
    client = s.client
    await s.fail(rejected(403, 'actor_mismatch'))
    expect(cacheAtClear).toBeUndefined()
  })

  it('거절이 늦게 도착해도 행위자 확인 화면이 구독 중인 목록 요청은 남긴다', async () => {
    const { client, fail } = setup()
    const pendingKey = [...LOGIN_ACTORS_KEY, 'pending']
    const observer = new QueryObserver(client, { queryKey: pendingKey, queryFn: () => new Promise<string[]>(() => {}), retry: false })
    const unsubscribe = observer.subscribe(() => {})
    await fail(rejected(403, 'actor_mismatch'))
    expect(client.getQueryCache().find({ queryKey: pendingKey })).toBeDefined()
    expect(observer.getCurrentResult().fetchStatus).toBe('fetching')
    unsubscribe()
  })

  it('다른 400 오류는 행위자 거절로 보지 않고 저장값과 목록 캐시를 둔다', async () => {
    const { storage, client, onActorCleared, fail } = setup()
    await fail(rejected(400, 'invalid_field'))
    expect(onActorCleared).not.toHaveBeenCalled()
    expect(storage.getActor()).toBe('gone')
    expect(client.getQueryData(LIST_KEY)).toEqual(['me', 'boss'])
  })

  it('401은 토큰 거부로 알린다', async () => {
    const { storage, onUnauthorized, onActorCleared, fail } = setup()
    await fail(new ApiError(401, 'unauthorized', '거부'))
    expect(onUnauthorized).toHaveBeenCalledTimes(1)
    expect(onActorCleared).not.toHaveBeenCalled()
    expect(storage.getActor()).toBe('gone')
  })
})

describe('refreshForActor', () => {
  async function observed() {
    const { client } = setup()
    const queryFn = vi.fn(() => Promise.resolve('ok'))
    const observer = new QueryObserver(client, { queryKey: ['agb', 'projects'], queryFn, retry: false })
    const unsubscribe = observer.subscribe(() => {})
    await vi.waitFor(() => expect(queryFn).toHaveBeenCalledTimes(1))
    return { client, queryFn, unsubscribe }
  }

  it('행위자가 정해지면 구독 중인 쿼리를 다시 받는다', async () => {
    const { client, queryFn, unsubscribe } = await observed()
    refreshForActor(client, 'boss')
    await vi.waitFor(() => expect(queryFn).toHaveBeenCalledTimes(2))
    unsubscribe()
  })

  it('행위자가 없으면 다시 받지 않는다', async () => {
    const { client, queryFn, unsubscribe } = await observed()
    refreshForActor(client, null)
    await new Promise((r) => setTimeout(r, 20))
    expect(queryFn).toHaveBeenCalledTimes(1)
    unsubscribe()
  })
})

describe('createQueryClient 폴백 폴링과 스트림 연결 상태', () => {
  it('stream이 connected면 refetchInterval이 false를, 그 외에는 FALLBACK_POLL_MS를 돌려준다', () => {
    connection.reset()
    const { client } = setup()
    const refetchInterval = client.getDefaultOptions().queries?.refetchInterval
    expect(typeof refetchInterval).toBe('function')
    const evalInterval = refetchInterval as () => number | false

    expect(connection.getSnapshot().stream).toBe('idle')
    expect(evalInterval()).toBe(FALLBACK_POLL_MS)

    connection.markStreamConnecting()
    expect(connection.getSnapshot().stream).toBe('connecting')
    expect(evalInterval()).toBe(FALLBACK_POLL_MS)

    connection.markStreamConnected()
    expect(connection.getSnapshot().stream).toBe('connected')
    expect(evalInterval()).toBe(false)

    connection.markStreamReconnecting(new NetworkError())
    expect(connection.getSnapshot().stream).toBe('reconnecting')
    expect(connection.getSnapshot().error?.kind).toBe('network')
    expect(evalInterval()).toBe(FALLBACK_POLL_MS)

    connection.markStreamReconnecting(new ServerUnavailableError(503))
    expect(connection.getSnapshot().error?.kind).toBe('server')
    expect(evalInterval()).toBe(FALLBACK_POLL_MS)

    connection.markOk()
    expect(connection.getSnapshot().stream).toBe('reconnecting')
    expect(connection.getSnapshot().error).toBeNull()

    connection.reset()
    expect(connection.getSnapshot()).toEqual({ lastOkAt: null, error: null, stream: 'idle' })
  })
})

describe('createQueryClient 스트림 상태 전환에 따른 폴백 폴링 동기화', () => {
  const defaultIsServer = environmentManager.isServer()

  beforeEach(() => {
    environmentManager.setIsServer(() => false)
    connection.reset()
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
    connection.reset()
    environmentManager.setIsServer(() => defaultIsServer)
  })

  async function observeQuery(
    client: ReturnType<typeof setup>['client'],
    queryKey: readonly unknown[],
    options?: { refetchInterval?: false },
  ) {
    const queryFn = vi.fn(() => Promise.resolve('ok'))
    const observer = new QueryObserver(client, { queryKey, queryFn, retry: false, ...options })
    const unsubscribe = observer.subscribe(() => {})
    try {
      await vi.advanceTimersByTimeAsync(0)
      expect(queryFn).toHaveBeenCalledTimes(1)
    } catch (err) {
      unsubscribe()
      throw err
    }
    return { queryFn, unsubscribe }
  }

  async function assertReconnectingPollingLifecycle(client: ReturnType<typeof setup>['client'], queryKey: readonly unknown[]) {
    connection.markStreamConnected()
    const { queryFn, unsubscribe } = await observeQuery(client, queryKey)

    try {
      await vi.advanceTimersByTimeAsync(FALLBACK_POLL_MS * 4)
      expect(queryFn).toHaveBeenCalledTimes(1)

      connection.markStreamReconnecting()
      await vi.advanceTimersByTimeAsync(FALLBACK_POLL_MS)
      expect(queryFn).toHaveBeenCalledTimes(2)

      await vi.advanceTimersByTimeAsync(FALLBACK_POLL_MS)
      expect(queryFn).toHaveBeenCalledTimes(3)

      connection.markStreamConnected()
      await vi.advanceTimersByTimeAsync(FALLBACK_POLL_MS * 4)
      expect(queryFn).toHaveBeenCalledTimes(3)
    } finally {
      unsubscribe()
    }
  }

  it('연결 중 구독한 뒤 connected로 전환되면 폴백 폴링을 중단한다', async () => {
    connection.markStreamConnecting()
    const { client } = setup()
    const { queryFn, unsubscribe } = await observeQuery(client, ['agb', 'issues'])

    try {
      await vi.advanceTimersByTimeAsync(5000)
      expect(queryFn).toHaveBeenCalledTimes(1)

      connection.markStreamConnected()
      await vi.advanceTimersByTimeAsync(FALLBACK_POLL_MS * 4)
      expect(queryFn).toHaveBeenCalledTimes(1)
    } finally {
      unsubscribe()
    }
  })

  it('connected 중 구독한 뒤 reconnecting으로 전환되면 폴백 폴링을 시작하고 재연결 시 중단한다', async () => {
    const { client } = setup()
    await assertReconnectingPollingLifecycle(client, ['agb', 'projects'])
  })

  it('개별 refetchInterval: false 옵션은 reconnecting으로 전환돼도 유지된다', async () => {
    connection.markStreamConnected()
    const { client } = setup()
    const { queryFn, unsubscribe } = await observeQuery(client, ['agb', 'static'], { refetchInterval: false })

    try {
      connection.markStreamReconnecting()
      await vi.advanceTimersByTimeAsync(FALLBACK_POLL_MS * 2)
      expect(queryFn).toHaveBeenCalledTimes(1)
    } finally {
      unsubscribe()
    }
  })

  it('mount와 unmount로 재구독한 뒤에도 connected에서 reconnecting 전환 시 폴링이 동작한다', async () => {
    const { client } = setup()
    client.mount()
    client.unmount()
    client.mount()

    try {
      await assertReconnectingPollingLifecycle(client, ['agb', 'remount'])
    } finally {
      client.unmount()
    }
  })

  it('unmount 동안 스트림 상태가 바뀐 뒤 다시 mount하면 폴링 타이머를 즉시 동기화한다', async () => {
    connection.markStreamConnected()
    const { client } = setup()
    client.mount()
    const { queryFn, unsubscribe } = await observeQuery(client, ['agb', 'unmount-change'])

    try {
      client.unmount()
      connection.markStreamReconnecting()
      client.mount()

      await vi.advanceTimersByTimeAsync(FALLBACK_POLL_MS)
      expect(queryFn).toHaveBeenCalledTimes(2)
    } finally {
      unsubscribe()
      client.unmount()
    }
  })
})
