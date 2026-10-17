import { useSyncExternalStore } from 'react'
import { NetworkError, ServerUnavailableError } from '@/api/client'

export type ConnectionError = { kind: 'network' | 'server'; message: string }

export interface ConnectionStatus {
  lastOkAt: number | null
  error: ConnectionError | null
}

let status: ConnectionStatus = { lastOkAt: null, error: null }
const listeners = new Set<() => void>()

function emit(next: ConnectionStatus) {
  status = next
  for (const l of listeners) l()
}

/** 조회 성공·실패를 모아 사이드바의 연결 상태로 보여 주는 작은 저장소. 3초마다 갱신되어도 구독한 컴포넌트만 다시 그린다. */
export const connection = {
  subscribe(listener: () => void) {
    listeners.add(listener)
    return () => void listeners.delete(listener)
  },
  getSnapshot() {
    return status
  },
  markOk() {
    emit({ lastOkAt: Date.now(), error: null })
  },
  /** 네트워크 오류와 서버 꺼짐만 전역 연결 상태로 본다. 404 같은 API 오류는 화면마다 처리한다. */
  markError(err: unknown) {
    if (err instanceof NetworkError) {
      emit({ ...status, error: { kind: 'network', message: '네트워크 오류: 개발 서버나 호스트에 닿지 못했습니다.' } })
    } else if (err instanceof ServerUnavailableError) {
      emit({ ...status, error: { kind: 'server', message: 'API 서버가 꺼져 있거나 응답하지 않습니다.' } })
    }
  },
  reset() {
    emit({ lastOkAt: null, error: null })
  },
}

export function useConnection(): ConnectionStatus {
  return useSyncExternalStore(connection.subscribe, connection.getSnapshot)
}
