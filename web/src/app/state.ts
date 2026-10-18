import { createContext, useContext } from 'react'
import type { ApiClient } from '@/api/client'
import type { Instance } from '@/lib/instances'

export const ACTOR = 'owner'
export const CLIENT_HOST = 'web'
export const POLL_MS = 3000

export interface AppState {
  instances: Instance[]
  instance: Instance
  token: string | null
  project: string | null
  sessionId: string
  /** 401로 로그아웃된 뒤 로그인 화면에 보여 줄 안내 */
  authNotice: string | null
  selectInstance: (id: string) => void
  login: (token: string) => void
  logout: () => void
  selectProject: (key: string | null) => void
}

export const AppStateContext = createContext<AppState | null>(null)
export const ApiContext = createContext<ApiClient | null>(null)

export function useAppState(): AppState {
  const ctx = useContext(AppStateContext)
  if (!ctx) throw new Error('AppStateProvider 밖에서 useAppState를 불렀습니다.')
  return ctx
}

/** 로그인된 화면 안에서만 쓴다. 토큰이 없으면 상위에서 로그인 화면을 보여 주므로 여기 오지 않는다. */
export function useApi(): ApiClient {
  const client = useContext(ApiContext)
  if (!client) throw new Error('토큰이 없는 상태에서 API 클라이언트를 요청했습니다.')
  return client
}
