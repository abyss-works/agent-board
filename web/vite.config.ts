import type { IncomingMessage, ServerResponse } from 'node:http'
import type { Socket } from 'node:net'
import { fileURLToPath } from 'node:url'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import type { ProxyOptions } from 'vite'
import { defineConfig } from 'vitest/config'

// 개발 서버 프록시. 브라우저가 같은 출처(/api/<인스턴스>/v1/...)로 부르면 경로의 접두어를 떼고
// 해당 인스턴스로 넘긴다. 토큰과 추적 헤더는 브라우저가 직접 보내며 프록시는 아무것도 주입하지 않는다.
const instances = {
  local: 'http://127.0.0.1:8420',
  staging: 'http://127.0.0.1:8421',
} as const

function proxyFor(name: keyof typeof instances): ProxyOptions {
  const target = instances[name]
  const prefix = `/api/${name}`
  return {
    target,
    changeOrigin: true,
    rewrite: (path) => path.slice(prefix.length),
    configure: (proxy) => {
      // 대상 서버가 꺼져 있으면 http-proxy의 기본 500 대신 JSON 502를 돌려주어
      // 화면이 "서버 꺼짐"을 네트워크 오류와 구분할 수 있게 한다.
      proxy.on('error', (_err: Error, _req: IncomingMessage, res: ServerResponse | Socket) => {
        if ('writeHead' in res && !res.headersSent) {
          res.writeHead(502, { 'Content-Type': 'application/json; charset=utf-8' })
          res.end(
            JSON.stringify({
              error: { code: 'upstream_unavailable', message: `${target}에 연결할 수 없습니다.` },
            }),
          )
        }
      })
    },
  }
}

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  server: {
    host: '127.0.0.1',
    port: 5173,
    strictPort: true,
    proxy: {
      '/api/local': proxyFor('local'),
      '/api/staging': proxyFor('staging'),
    },
  },
  test: {
    environment: 'node',
    include: ['src/**/*.test.{ts,tsx}'],
  },
})
