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

/** 토큰(인스턴스별), 세션 ID(브라우저당 하나), 선택한 인스턴스와 프로젝트를 둔다. */
export class AppStorage {
  private readonly store: KeyValueStore
  private sessionId: string | null = null

  constructor(store: KeyValueStore) {
    this.store = store
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

  getToken(instanceId: string): string | null {
    return this.read(`token.${instanceId}`)
  }

  setToken(instanceId: string, token: string): void {
    this.write(`token.${instanceId}`, token)
  }

  clearToken(instanceId: string): void {
    this.write(`token.${instanceId}`, null)
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

  getInstance(): string | null {
    return this.read('instance')
  }

  setInstance(id: string): void {
    this.write('instance', id)
  }

  getProject(instanceId: string): string | null {
    return this.read(`project.${instanceId}`)
  }

  setProject(instanceId: string, key: string | null): void {
    this.write(`project.${instanceId}`, key)
  }
}
