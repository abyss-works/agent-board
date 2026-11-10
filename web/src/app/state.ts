import { createContext, useContext } from 'react'
import type { ApiClient } from '@/api/client'

export const CLIENT_HOST = 'web'
export const POLL_MS = 3000
/** API 기준 경로. 개발 서버에서는 Vite 프록시(`/api`), 운영 빌드에서는 같은 출처다. */
export const BASE_URL = import.meta.env.DEV ? '/api' : ''

export interface AppState {
  token: string | null
  /** 요청의 행위자. 토큰만 저장된 옛 브라우저는 목록을 받아 정하기 전까지 null이다. */
  actor: string | null
  project: string | null
  sessionId: string
  /** 401로 로그아웃된 뒤 로그인 화면에 보여 줄 안내 */
  authNotice: string | null
  login: (token: string, actor: string) => void
  logout: () => void
  selectActor: (actor: string) => void
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
