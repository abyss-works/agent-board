import { QueryObserver } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/client'
import { AppStorage, memoryStore, type KeyValueStore } from '@/lib/storage'
import { createQueryClient, LOGIN_ACTORS_KEY, refreshForActor } from './query-client'

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
