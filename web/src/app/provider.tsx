import { QueryClientProvider } from '@tanstack/react-query'
import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { ApiClient } from '@/api/client'
import { AppStorage, browserStore } from '@/lib/storage'
import { connection } from './connection'
import { createQueryClient, refreshForActor } from './query-client'
import { ApiContext, BASE_URL, AppStateContext, CLIENT_HOST, type AppState } from './state'

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

  // 서버가 행위자를 거절하면 query-client가 저장값을 지운 뒤 부른다. 여기서는 화면 상태만 비워 행위자 확인 화면으로 돌아간다. 토큰은 둔다.
  const handleActorCleared = useCallback(() => setActor(null), [])

  const [queryClient] = useState(() => createQueryClient({ storage, onUnauthorized: handleUnauthorized, onActorCleared: handleActorCleared }))

  // 행위자가 바뀌면 이전 행위자로 받은 캐시를 다시 받는다. 클라이언트가 새 행위자로 바뀐 뒤에 실행된다.
  useEffect(() => {
    refreshForActor(queryClient, actor)
  }, [actor, queryClient])

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
