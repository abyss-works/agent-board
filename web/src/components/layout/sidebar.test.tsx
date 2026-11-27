import { renderToStaticMarkup } from 'react-dom/server'
import { afterEach, describe, expect, it } from 'vitest'
import { NetworkError } from '@/api/client'
import { connection } from '@/app/connection'
import { ConnectionStatus } from './sidebar'

afterEach(() => {
  connection.reset()
})

describe('ConnectionStatus', () => {
  it('스트림 연결 상태에 따라 실시간 연결 문구와 점 색상을 표시한다', () => {
    connection.reset()
    const idleHtml = renderToStaticMarkup(<ConnectionStatus />)
    expect(idleHtml).toContain('실시간 연결 중')
    expect(idleHtml).toContain('bg-sky-400')
    expect(idleHtml).not.toContain('초마다 갱신')

    connection.markStreamConnecting()
    const connectingHtml = renderToStaticMarkup(<ConnectionStatus />)
    expect(connectingHtml).toContain('실시간 연결 중')
    expect(connectingHtml).toContain('bg-sky-400')

    connection.markStreamConnected()
    const connectedHtml = renderToStaticMarkup(<ConnectionStatus />)
    expect(connectedHtml).toContain('실시간 연결')
    expect(connectedHtml).not.toContain('실시간 연결 중')
    expect(connectedHtml).toContain('bg-emerald-400')
    expect(connectedHtml).toContain('tabular-nums')

    connection.markStreamReconnecting()
    const reconnectingHtml = renderToStaticMarkup(<ConnectionStatus />)
    expect(reconnectingHtml).toContain('다시 연결 중')
    expect(reconnectingHtml).toContain('bg-amber-400')
  })

  it('전역 연결 오류가 있으면 오류 문구를 표시한다', () => {
    connection.markStreamReconnecting(new NetworkError())
    const html = renderToStaticMarkup(<ConnectionStatus />)
    expect(html).toContain('bg-rose-400')
    expect(html).toContain('네트워크 오류')
  })
})
