import { MutationCache, QueryCache, QueryClient, notifyManager } from '@tanstack/react-query'
import { isActorRejected, isApiError, isUnauthorized } from '@/api/client'
import { DEVICE_AUTHORIZATION_KEY, QUERY_ROOT } from '@/api/queries'
import type { AppStorage } from '@/lib/storage'
import { connection } from './connection'
import { FALLBACK_POLL_MS } from './state'

/** 로그인 직후와 행위자 확인 화면이 받는 사용자 행위자 목록의 쿼리 키 앞부분. */
export const LOGIN_ACTORS_KEY = [QUERY_ROOT, 'login-actors']

interface Handlers {
  storage: AppStorage
  onUnauthorized: () => void
  /** 저장된 행위자를 지운 뒤 화면 상태를 맞추려고 부른다. */
  onActorCleared: () => void
}

/** 스트림 연결 상태가 바뀔 때 활성 옵저버가 폴링 간격을 즉시 다시 계산하도록 알린다. */
function syncStreamPolling(client: QueryClient): void {
  notifyManager.batch(() => {
    for (const query of client.getQueryCache().getAll()) {
      for (const observer of [...query.observers]) {
        observer.onQueryUpdate()
      }
    }
  })
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
        refetchInterval: () => (connection.getSnapshot().stream === 'connected' ? false : FALLBACK_POLL_MS),
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

  let prevStream = connection.getSnapshot().stream
  const subscribeConnection = () =>
    connection.subscribe(() => {
      const nextStream = connection.getSnapshot().stream
      if (nextStream === prevStream) return
      prevStream = nextStream
      syncStreamPolling(client)
    })

  let unsubscribeConnection: (() => void) | null = subscribeConnection()
  let mountCount = 0
  const baseMount = client.mount.bind(client)
  const baseUnmount = client.unmount.bind(client)

  client.mount = () => {
    mountCount += 1
    baseMount()
    if (!unsubscribeConnection) {
      const nextStream = connection.getSnapshot().stream
      const changed = nextStream !== prevStream
      prevStream = nextStream
      unsubscribeConnection = subscribeConnection()
      if (changed) syncStreamPolling(client)
    }
  }

  client.unmount = () => {
    if (mountCount > 0) mountCount -= 1
    if (mountCount === 0) {
      unsubscribeConnection?.()
      unsubscribeConnection = null
    }
    baseUnmount()
  }

  return client
}

/** 행위자가 정해지면 이전 행위자로 받은 캐시를 새 행위자의 클라이언트로 다시 받는다. */
export function refreshForActor(client: QueryClient, actor: string | null): void {
  if (actor) void client.invalidateQueries()
}

/** 커서 없는 재연결 시 기기 승인 단건 조회(`refetchOnReconnect: false`)를 빼고 캐시를 무효화한다. */
export function invalidateOnReconnect(client: QueryClient): void {
  void client.invalidateQueries({
    predicate: (query) =>
      !(query.queryKey[0] === DEVICE_AUTHORIZATION_KEY[0] && query.queryKey[1] === DEVICE_AUTHORIZATION_KEY[1]),
  })
}
