export interface KeyValueStore {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
  removeItem(key: string): void
}

const PREFIX = 'agb.'

export function memoryStore(): KeyValueStore {
  const m = new Map<string, string>()
  return {
    getItem: (k) => m.get(k) ?? null,
    setItem: (k, v) => void m.set(k, v),
    removeItem: (k) => void m.delete(k),
  }
}

/** 브라우저 localStorage. 접근이 막혀 있으면 메모리 저장소로 대신한다. */
export function browserStore(): KeyValueStore {
  try {
    const ls = window.localStorage
    ls.getItem(`${PREFIX}probe`)
    return ls
  } catch {
    return memoryStore()
  }
}

/** `web-` + 16바이트 난수의 16진수. 추적 헤더 X-Session-Id 규칙에 맞는다. */
export function newSessionId(random: (n: number) => Uint8Array = randomBytes): string {
  const hex = Array.from(random(16), (b) => b.toString(16).padStart(2, '0')).join('')
  return `web-${hex}`
}

function randomBytes(n: number): Uint8Array {
  const bytes = new Uint8Array(n)
  crypto.getRandomValues(bytes)
  return bytes
}

/** 토큰, 세션 ID(브라우저당 하나), 선택한 프로젝트를 둔다. */
export class AppStorage {
  private readonly store: KeyValueStore
  private sessionId: string | null = null

  constructor(store: KeyValueStore) {
    this.store = store
    this.migrateInstanceKeys()
  }

  /**
   * 인스턴스별 키(`token.<ID>`, `project.<ID>`, `instance`)를 쓰던 옛 판의 값을 옮기고 지운다.
   * 운영 빌드의 ID는 `default`라서 그 값만 새 키로 옮기고, 개발 모드 ID(`local`, `staging`, `prod`)의
   * 값은 새 프록시 대상과 같은 서버인지 알 수 없어 옮기지 않고 지운다. 새 키가 있으면 덮어쓰지 않는다.
   */
  private migrateInstanceKeys(): void {
    for (const name of ['token', 'project']) {
      const legacy = this.read(`${name}.default`)
      if (legacy !== null && this.read(name) === null) this.write(name, legacy)
      for (const id of ['default', 'local', 'staging', 'prod']) this.write(`${name}.${id}`, null)
    }
    this.write('instance', null)
  }

  private read(key: string): string | null {
    try {
      return this.store.getItem(PREFIX + key)
    } catch {
      return null
    }
  }

  private write(key: string, value: string | null): void {
    try {
      if (value === null) this.store.removeItem(PREFIX + key)
      else this.store.setItem(PREFIX + key, value)
    } catch {
      // 저장은 편의 기능이다. 막혀 있어도 화면은 동작한다.
    }
  }

  getToken(): string | null {
    return this.read('token')
  }

  setToken(token: string): void {
    this.write('token', token)
  }

  clearToken(): void {
    this.write('token', null)
  }

  getSessionId(): string {
    if (this.sessionId) return this.sessionId
    const stored = this.read('session')
    if (stored) {
      this.sessionId = stored
      return stored
    }
    const created = newSessionId()
    this.write('session', created)
    this.sessionId = created
    return created
  }

  getProject(): string | null {
    return this.read('project')
  }

  setProject(key: string | null): void {
    this.write('project', key)
  }
}
