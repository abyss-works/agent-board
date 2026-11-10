import { MutationCache, QueryCache, QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useCallback, useMemo, useState, type ReactNode } from 'react'
import { ApiClient, isActorRejected, isApiError, isUnauthorized } from '@/api/client'
import { AppStorage, browserStore } from '@/lib/storage'
import { connection } from './connection'
import { ApiContext, BASE_URL, AppStateContext, CLIENT_HOST, POLL_MS, type AppState } from './state'

const storage = new AppStorage(browserStore())

export function AppStateProvider({ children }: { children: ReactNode }) {
  const [token, setToken] = useState<string | null>(() => storage.getToken())
  const [actor, setActor] = useState<string | null>(() => storage.getActor())
  const [project, setProject] = useState<string | null>(() => storage.getProject())
  const [authNotice, setAuthNotice] = useState<string | null>(null)
  const sessionId = useMemo(() => storage.getSessionId(), [])

  const handleUnauthorized = useCallback(() => {
    storage.clearToken()
    setToken(null)
    setAuthNotice('서버가 토큰을 거부했습니다(401). 토큰을 다시 입력해 주세요.')
  }, [])

  // 저장된 행위자를 서버가 더는 받지 않으면 지운다. 토큰은 두고 행위자 확인 화면으로 돌아간다.
  const handleActorRejected = useCallback(() => {
    storage.clearActor()
    setActor(null)
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
            else if (isActorRejected(err)) handleActorRejected()
            else connection.markError(err)
          },
        }),
        mutationCache: new MutationCache({
          onSuccess: () => connection.markOk(),
          onError: (err) => {
            if (isUnauthorized(err)) handleUnauthorized()
            else if (isActorRejected(err)) handleActorRejected()
            else connection.markError(err)
          },
        }),
      }),
  )

  const login = useCallback((value: string, picked: string) => {
    const trimmed = value.trim()
    storage.setToken(trimmed)
    storage.setActor(picked)
    setToken(trimmed)
    setActor(picked)
    setAuthNotice(null)
    connection.reset()
  }, [])

  const logout = useCallback(() => {
    storage.clearToken()
    setToken(null)
    setAuthNotice(null)
    connection.reset()
    queryClient.clear()
  }, [queryClient])

  const selectActor = useCallback((picked: string) => {
    storage.setActor(picked)
    setActor(picked)
  }, [])

  const selectProject = useCallback((key: string | null) => {
    storage.setProject(key)
    setProject(key)
  }, [])

  const client = useMemo(
    () =>
      token && actor
        ? new ApiClient({ baseUrl: BASE_URL, token, actor, sessionId, clientHost: CLIENT_HOST })
        : null,
    [token, actor, sessionId],
  )

  const state = useMemo<AppState>(
    () => ({ token, actor, project, sessionId, authNotice, login, logout, selectActor, selectProject }),
    [token, actor, project, sessionId, authNotice, login, logout, selectActor, selectProject],
  )

  return (
    <QueryClientProvider client={queryClient}>
      <AppStateContext.Provider value={state}>
        <ApiContext.Provider value={client}>{children}</ApiContext.Provider>
      </AppStateContext.Provider>
    </QueryClientProvider>
  )
}
