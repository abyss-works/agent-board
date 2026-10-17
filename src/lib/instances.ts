export interface Instance {
  id: string
  label: string
  /** 요청 기준 경로. 뒤에 `/v1/...`이 붙는다. */
  baseUrl: string
  /** 화면에 보이는 대상 주소 */
  description: string
}

/**
 * 개발 서버에서는 Vite 프록시 뒤의 두 인스턴스(기록용, 스테이징)를, 운영 빌드에서는
 * 같은 출처의 `/v1`을 부르는 인스턴스 하나를 쓴다.
 */
export function instancesFor(dev: boolean, host: string): Instance[] {
  if (dev) {
    return [
      { id: 'local', label: '기록용', baseUrl: '/api/local', description: '127.0.0.1:8420' },
      { id: 'staging', label: '스테이징', baseUrl: '/api/staging', description: '127.0.0.1:8421' },
    ]
  }
  return [{ id: 'default', label: host, baseUrl: '', description: host }]
}
