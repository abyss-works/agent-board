import { MutationCache, QueryCache, QueryClient } from '@tanstack/react-query'
import { isActorRejected, isApiError, isUnauthorized } from '@/api/client'
import { QUERY_ROOT } from '@/api/queries'
import type { AppStorage } from '@/lib/storage'
import { connection } from './connection'
import { POLL_MS } from './state'

/** 로그인 직후와 행위자 확인 화면이 받는 사용자 행위자 목록의 쿼리 키 앞부분. */
export const LOGIN_ACTORS_KEY = [QUERY_ROOT, 'login-actors']

interface Handlers {
  storage: AppStorage
  onUnauthorized: () => void
  /** 저장된 행위자를 지운 뒤 화면 상태를 맞추려고 부른다. */
  onActorCleared: () => void
}

/**
 * 행위자를 서버가 거절하면 로그인용 행위자 목록 캐시를 버린 뒤 저장된 행위자를 지우고 `onActorCleared`를 부른다.
 * 행위자 확인 화면이 오래된 목록으로 먼저 정하지 않고 새로 받은 목록으로만 정하게 하려는 것이다.
 * 구독자가 없는 캐시만 버려, 행위자 확인 화면이 이미 받고 있는 요청은 남긴다. 캐시를 먼저 버리는 것은 `onActorCleared`에서 화면이 바뀌므로, 그때는 오래된 목록이 이미 없어야 하기 때문이다.
 */
export function createQueryClient({ storage, onUnauthorized, onActorCleared }: Handlers): QueryClient {
  const onError = (err: unknown) => {
    if (isUnauthorized(err)) onUnauthorized()
    else if (isActorRejected(err)) {
      client.removeQueries({ queryKey: LOGIN_ACTORS_KEY, type: 'inactive' })
      storage.clearActor()
      onActorCleared()
    } else connection.markError(err)
  }
  const client: QueryClient = new QueryClient({
    defaultOptions: {
      queries: {
        refetchInterval: POLL_MS,
        refetchIntervalInBackground: false,
        staleTime: 1000,
        retry: (count, err) => !isApiError(err) && count < 1,
        retryDelay: 1000,
      },
      mutations: { retry: false },
    },
    queryCache: new QueryCache({ onSuccess: () => connection.markOk(), onError }),
    mutationCache: new MutationCache({ onSuccess: () => connection.markOk(), onError }),
  })
  return client
}

/** 행위자가 정해지면 이전 행위자로 받은 캐시를 새 행위자의 클라이언트로 다시 받는다. */
export function refreshForActor(client: QueryClient, actor: string | null): void {
  if (actor) void client.invalidateQueries()
}
