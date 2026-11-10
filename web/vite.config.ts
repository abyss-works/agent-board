import type { IncomingMessage, ServerResponse } from 'node:http'
import type { Socket } from 'node:net'
import { fileURLToPath } from 'node:url'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import type { ProxyOptions } from 'vite'
import { loadEnv } from 'vite'
import { defineConfig } from 'vitest/config'

// 개발 서버 프록시. 브라우저가 같은 출처(/api/v1/...)로 부르면 접두어 /api를 떼고 대상 서버로 넘긴다.
// 대상은 환경변수 AGB_PROXY_TARGET이고 기본값은 로컬 서버다. VITE_ 접두어가 없으므로 클라이언트 번들에 들어가지 않는다. 토큰과 추적 헤더는 브라우저가
// 직접 보내며 프록시는 아무것도 주입하지 않는다.
const DEFAULT_TARGET = 'http://127.0.0.1:8420'

function proxyFor(target: string): ProxyOptions {
  const prefix = '/api'
  return {
    target,
    changeOrigin: true,
    secure: true,
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

export default defineConfig(({ mode }) => {
  const target = loadEnv(mode, process.cwd(), 'AGB_PROXY_').AGB_PROXY_TARGET || DEFAULT_TARGET
  return {
    plugins: [react(), tailwindcss()],
    resolve: {
      alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
    },
    server: {
      host: '127.0.0.1',
      port: 5173,
      strictPort: true,
      proxy: { '/api': proxyFor(target) },
    },
    test: {
      environment: 'node',
      include: ['src/**/*.test.{ts,tsx}'],
    },
  }
})
