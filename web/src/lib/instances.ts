export interface Instance {
  id: string
  label: string
  /** 요청 기준 경로. 뒤에 `/v1/...`이 붙는다. */
  baseUrl: string
  /** 화면에 보이는 대상 주소 */
  description: string
}

/**
 * 인스턴스는 항상 하나다. 개발 서버에서는 Vite 프록시(`/api`) 뒤의 서버를, 운영 빌드에서는
 * 같은 출처의 `/v1`을 부른다.
 */
export function instancesFor(dev: boolean, host: string): Instance[] {
  return [{ id: 'default', label: host, baseUrl: dev ? '/api' : '', description: host }]
}
