import { MutationCache, QueryCache, QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useCallback, useMemo, useState, type ReactNode } from 'react'
import { ApiClient, isApiError, isUnauthorized } from '@/api/client'
import { instancesFor, type Instance } from '@/lib/instances'
import { AppStorage, browserStore } from '@/lib/storage'
import { connection } from './connection'
import { ACTOR, ApiContext, AppStateContext, CLIENT_HOST, POLL_MS, type AppState } from './state'

const storage = new AppStorage(browserStore())
const instances = instancesFor(import.meta.env.DEV, window.location.host)

/** 저장소에 적힌 선택 인스턴스. 상태 갱신 전에 저장소를 먼저 쓰므로 콜백에서 최신 값을 읽는 데 쓴다. */
function currentInstanceId(): string {
  const saved = storage.getInstance()
  return instances.some((i) => i.id === saved) ? (saved as string) : instances[0].id
}

function initialInstance(): Instance {
  const id = currentInstanceId()
  return instances.find((i) => i.id === id) ?? instances[0]
}

export function AppStateProvider({ children }: { children: ReactNode }) {
  const [instance, setInstance] = useState<Instance>(initialInstance)
  const [token, setToken] = useState<string | null>(() => storage.getToken(initialInstance().id))
  const [project, setProject] = useState<string | null>(() => storage.getProject(initialInstance().id))
  const [authNotice, setAuthNotice] = useState<string | null>(null)
  const sessionId = useMemo(() => storage.getSessionId(), [])

  const handleUnauthorized = useCallback(() => {
    storage.clearToken(currentInstanceId())
    setToken(null)
    setAuthNotice('서버가 토큰을 거부했습니다(401). 토큰을 다시 입력해 주세요.')
  }, [])

  const [queryClient] = useState(
    () =>
      new QueryClient({
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
        queryCache: new QueryCache({
          onSuccess: () => connection.markOk(),
          onError: (err) => {
            if (isUnauthorized(err)) handleUnauthorized()
            else connection.markError(err)
          },
        }),
        mutationCache: new MutationCache({
          onSuccess: () => connection.markOk(),
          onError: (err) => {
            if (isUnauthorized(err)) handleUnauthorized()
            else connection.markError(err)
          },
        }),
      }),
  )

  const selectInstance = useCallback(
    (id: string) => {
      const next = instances.find((i) => i.id === id)
      if (!next || next.id === currentInstanceId()) return
      storage.setInstance(next.id)
      setInstance(next)
      setToken(storage.getToken(next.id))
      setProject(storage.getProject(next.id))
      setAuthNotice(null)
      connection.reset()
      queryClient.clear()
    },
    [queryClient],
  )

  const login = useCallback((value: string) => {
    const trimmed = value.trim()
    storage.setToken(currentInstanceId(), trimmed)
    setToken(trimmed)
    setAuthNotice(null)
    connection.reset()
  }, [])

  const logout = useCallback(() => {
    storage.clearToken(currentInstanceId())
    setToken(null)
    setAuthNotice(null)
    connection.reset()
    queryClient.clear()
  }, [queryClient])

  const selectProject = useCallback((key: string | null) => {
    storage.setProject(currentInstanceId(), key)
    setProject(key)
  }, [])

  const client = useMemo(
    () =>
      token
        ? new ApiClient({ baseUrl: instance.baseUrl, token, actor: ACTOR, sessionId, clientHost: CLIENT_HOST })
        : null,
    [instance.baseUrl, token, sessionId],
  )

  const state = useMemo<AppState>(
    () => ({ instances, instance, token, project, sessionId, authNotice, selectInstance, login, logout, selectProject }),
    [instance, token, project, sessionId, authNotice, selectInstance, login, logout, selectProject],
  )

  return (
    <QueryClientProvider client={queryClient}>
      <AppStateContext.Provider value={state}>
        <ApiContext.Provider value={client}>{children}</ApiContext.Provider>
      </AppStateContext.Provider>
    </QueryClientProvider>
  )
}
