import { MutationCache, QueryCache, QueryClient } from '@tanstack/react-query'
import { isActorRejected, isApiError, isUnauthorized } from '@/api/client'
import { QUERY_ROOT } from '@/api/queries'
import { connection } from './connection'
import { POLL_MS } from './state'

/** 로그인 직후와 행위자 확인 화면이 받는 사용자 행위자 목록의 쿼리 키 앞부분. */
export const LOGIN_ACTORS_KEY = [QUERY_ROOT, 'login-actors']

interface Handlers {
  onUnauthorized: () => void
  onActorRejected: () => void
}

/**
 * 행위자를 서버가 거절하면 로그인용 행위자 목록 캐시를 버린 뒤 `onActorRejected`를 부른다.
 * 행위자 확인 화면이 오래된 목록으로 먼저 정하지 않고 새로 받은 목록으로만 정하게 하려는 것이다.
 */
export function createQueryClient({ onUnauthorized, onActorRejected }: Handlers): QueryClient {
  const onError = (err: unknown) => {
    if (isUnauthorized(err)) onUnauthorized()
    else if (isActorRejected(err)) {
      client.removeQueries({ queryKey: LOGIN_ACTORS_KEY })
      onActorRejected()
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
