import { describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/client'
import { AppStorage, memoryStore } from '@/lib/storage'
import { createQueryClient, LOGIN_ACTORS_KEY } from './query-client'

const rejected = (status: number, reason: string) => new ApiError(status, 'error', '거절', [{ reason }])

async function failWith(err: unknown, key: string[]) {
  const onUnauthorized = vi.fn()
  const onActorRejected = vi.fn()
  const client = createQueryClient({ onUnauthorized, onActorRejected })
  client.setQueryData([...LOGIN_ACTORS_KEY, 'token'], ['me', 'boss'])
  await client.fetchQuery({ queryKey: key, queryFn: () => Promise.reject(err), retry: false }).catch(() => {})
  return { client, onUnauthorized, onActorRejected }
}

describe('createQueryClient 행위자 거절', () => {
  it.each([
    [400, 'unknown_actor'],
    [403, 'actor_mismatch'],
  ])('%i %s이면 행위자 거절을 알리고 로그인용 행위자 목록 캐시를 버린다', async (status, reason) => {
    const { client, onActorRejected, onUnauthorized } = await failWith(rejected(status, reason), ['agb', 'projects'])
    expect(onActorRejected).toHaveBeenCalledTimes(1)
    expect(onUnauthorized).not.toHaveBeenCalled()
    expect(client.getQueryData([...LOGIN_ACTORS_KEY, 'token'])).toBeUndefined()
  })

  it('거절 처리는 저장된 행위자를 지워 행위자 확인 화면으로 가게 한다', async () => {
    const storage = new AppStorage(memoryStore())
    storage.setActor('gone')
    const client = createQueryClient({ onUnauthorized: () => {}, onActorRejected: () => storage.clearActor() })
    await client.fetchQuery({ queryKey: ['agb', 'x'], queryFn: () => Promise.reject(rejected(403, 'actor_mismatch')), retry: false }).catch(() => {})
    expect(storage.getActor()).toBeNull()
  })

  it('다른 400 오류는 행위자 거절로 보지 않고 목록 캐시도 둔다', async () => {
    const { client, onActorRejected } = await failWith(rejected(400, 'invalid_field'), ['agb', 'projects'])
    expect(onActorRejected).not.toHaveBeenCalled()
    expect(client.getQueryData([...LOGIN_ACTORS_KEY, 'token'])).toEqual(['me', 'boss'])
  })

  it('401은 토큰 거부로 알린다', async () => {
    const { onUnauthorized, onActorRejected } = await failWith(new ApiError(401, 'unauthorized', '거부'), ['agb', 'projects'])
    expect(onUnauthorized).toHaveBeenCalledTimes(1)
    expect(onActorRejected).not.toHaveBeenCalled()
  })
})
