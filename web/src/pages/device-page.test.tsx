import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router'
import { describe, expect, it } from 'vitest'
import { ApiClient } from '@/api/client'
import { deviceAuthorizationKey } from '@/api/queries'
import type { DeviceAuthorization } from '@/api/types'
import { ApiContext, AppStateContext, type AppState } from '@/app/state'
import { DevicePage } from './device-page'

const state: AppState = {
  token: 'token',
  actor: 'owner',
  project: 'APP',
  sessionId: 'web-session',
  authNotice: null,
  login: () => {},
  logout: () => {},
  selectActor: () => {},
  selectProject: () => {},
}

const api = new ApiClient({ baseUrl: '', token: 'token', actor: 'owner', sessionId: 'web-session', clientHost: 'web' }, () => {
  throw new Error('기기 승인 화면이 요청을 보냈습니다.')
})

function renderDevice(auth: DeviceAuthorization): string {
  const client = new QueryClient()
  client.setQueryData(deviceAuthorizationKey(auth.user_code), auth)
  return renderToStaticMarkup(
    <QueryClientProvider client={client}>
      <AppStateContext.Provider value={state}>
        <ApiContext.Provider value={api}>
          <MemoryRouter initialEntries={[`/device?code=${auth.user_code}`]}>
            <DevicePage />
          </MemoryRouter>
        </ApiContext.Provider>
      </AppStateContext.Provider>
    </QueryClientProvider>,
  )
}

function authFixture(partial: Partial<DeviceAuthorization> = {}): DeviceAuthorization {
  return {
    user_code: 'BDFG-HJKL',
    actor: 'codex',
    client_host: 'DESKTOP-RPLTFHM',
    status: 'pending',
    new_actor: false,
    created_at: '2026-10-19T01:20:00.000Z',
    expires_at: '2026-10-19T01:30:00.000Z',
    ...partial,
  }
}

describe('기기 승인 화면', () => {
  it('기존 에이전트 요청은 신규 에이전트 배지와 안내 문구를 표시하지 않는다', () => {
    const html = renderDevice(authFixture({ actor: 'codex', new_actor: false }))
    expect(html).toContain('codex')
    expect(html).not.toContain('신규 에이전트')
    expect(html).not.toContain('승인하면 새 agent 행위자로 자동 등록됩니다.')
  })

  it('미선언 에이전트의 대기 요청은 신규 에이전트 배지와 자동 등록 안내를 표시한다', () => {
    const html = renderDevice(authFixture({ actor: 'devin', status: 'pending', new_actor: true }))
    expect(html).toContain('devin')
    expect(html).toContain('신규 에이전트')
    expect(html).toContain('승인하면 새 agent 행위자로 자동 등록됩니다.')
  })

  it('신규 에이전트로 승인된 요청은 신규 에이전트 배지를 유지하고 대기 안내만 숨긴다', () => {
    const html = renderDevice(authFixture({ actor: 'devin', status: 'approved', new_actor: true }))
    expect(html).toContain('devin')
    expect(html).toContain('신규 에이전트')
    expect(html).toContain('CLI로 돌아가면 로그인이 끝납니다.')
    expect(html).not.toContain('승인하면 새 agent 행위자로 자동 등록됩니다.')
  })
})
