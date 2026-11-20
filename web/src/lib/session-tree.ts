import type { SessionListParams } from '@/api/client'
import type { Session, SessionRef } from '@/api/types'

export type SessionStatusFilter = 'all' | 'active' | 'waiting' | 'stale'

export const SESSION_STATUS_FILTERS: ReadonlyArray<{ value: SessionStatusFilter; label: string }> = [
  { value: 'all', label: '전체' },
  { value: 'active', label: '활성' },
  { value: 'waiting', label: '대기 중' },
  { value: 'stale', label: '멈춤' },
]

export function parseSessionStatusFilter(value: string | null): SessionStatusFilter {
  if (value === 'active' || value === 'waiting' || value === 'stale') return value
  return 'all'
}

/** 화면 필터 상태를 `GET /v1/sessions` 서버 질의 매개변수로 변환한다. */
export function sessionListParamsFor(opts: {
  project?: string
  named?: boolean
  status: SessionStatusFilter
  limit: number
}): SessionListParams {
  const params: SessionListParams = {
    project: opts.project,
    named: opts.named ? true : undefined,
    limit: opts.limit,
  }
  if (opts.status === 'active') params.active = true
  else if (opts.status === 'stale') params.active = false
  else if (opts.status === 'waiting') params.waiting = true
  return params
}

export interface SessionTreeNode {
  session: Session
  children: SessionTreeNode[]
  /** 부모 세션과 다른 호스트에서 실행 중인 자식이면 true다. */
  crossHost: boolean
}

export interface HostSessionGroup {
  host: string
  roots: SessionTreeNode[]
  totalCount: number
  activeCount: number
  waitingCount: number
  staleCount: number
  conflictCount: number
}

export interface SessionSummaryCounts {
  total: number
  active: number
  waiting: number
  stale: number
}

/** 세션을 유일하게 가리키는 내부 키. */
export function sessionKey(ref: Pick<SessionRef, 'actor' | 'session_id'>): string {
  return `${ref.actor}\0${ref.session_id}`
}

/** 사람이 읽는 `<actor>/<session_id>` 표기. */
export function sessionRefLabel(ref: Pick<SessionRef, 'actor' | 'session_id'>): string {
  return `${ref.actor}/${ref.session_id}`
}

/** 세션 관제 화면에서 특정 세션을 열어 보여 주는 경로. */
export function sessionDetailPath(ref: Pick<SessionRef, 'actor' | 'session_id'>): string {
  const params = new URLSearchParams({ actor: ref.actor, session: ref.session_id })
  return `/sessions?${params.toString()}`
}

function parseMillis(iso: string): number {
  const t = Date.parse(iso)
  return Number.isNaN(t) ? 0 : t
}

/**
 * 형제 세션 정렬 기준.
 * 대기 세션(`state !== null`) 먼저, 그다음 활성(`!stale`) 먼저, 그다음 `last_seen_at` 내림차순.
 */
export function compareSessions(a: Session, b: Session): number {
  const waitDiff = (a.state !== null ? 0 : 1) - (b.state !== null ? 0 : 1)
  if (waitDiff !== 0) return waitDiff
  const activeDiff = (a.stale ? 1 : 0) - (b.stale ? 1 : 0)
  if (activeDiff !== 0) return activeDiff
  const timeDiff = parseMillis(b.last_seen_at) - parseMillis(a.last_seen_at)
  if (timeDiff !== 0) return timeDiff
  const isoDiff = b.last_seen_at.localeCompare(a.last_seen_at)
  if (isoDiff !== 0) return isoDiff
  return a.actor.localeCompare(b.actor) || a.session_id.localeCompare(b.session_id)
}

export function summarizeSessions(sessions: readonly Session[]): SessionSummaryCounts {
  let active = 0
  let waiting = 0
  let stale = 0
  for (const s of sessions) {
    if (s.state !== null) waiting += 1
    if (s.stale) stale += 1
    else active += 1
  }
  return { total: sessions.length, active, waiting, stale }
}

export function filterSessionsByStatus(sessions: readonly Session[], filter: SessionStatusFilter): Session[] {
  switch (filter) {
    case 'active':
      return sessions.filter((s) => !s.stale)
    case 'waiting':
      return sessions.filter((s) => s.state !== null)
    case 'stale':
      return sessions.filter((s) => s.stale)
    default:
      return [...sessions]
  }
}

/**
 * 평면 세션 목록을 부모-자식 숲으로 묶은 뒤 각 루트의 `last_host`별로 호스트 그룹을 만든다.
 * - 부모가 목록에 없거나 자기 참조·순환에 속하면 루트로 승격한다.
 * - 부모-자식 호스트가 다르면 자식은 부모 아래에 유지하고 `crossHost: true`를 표시한다.
 * - 호스트 그룹은 대기 세션 보유 호스트 먼저, 그다음 활성 세션 보유 호스트 먼저, 같으면 호스트명 오름차순.
 */
export function buildHostSessionTrees(sessions: readonly Session[]): HostSessionGroup[] {
  const byKey = new Map<string, Session>()
  for (const s of sessions) {
    byKey.set(sessionKey(s), s)
  }

  const rawParentKey = (key: string): string | null => {
    const s = byKey.get(key)
    if (!s?.parent) return null
    const pKey = sessionKey(s.parent)
    if (pKey === key || !byKey.has(pKey)) return null
    return pKey
  }

  // 순환에 포함된 노드를 먼저 모두 찾아 루트로 승격한다(순환으로 진입하는 비순환 자식은 유지된다).
  const cycleNodes = new Set<string>()
  const done = new Set<string>()
  for (const start of byKey.keys()) {
    if (done.has(start)) continue
    const path: string[] = []
    const pathIndex = new Map<string, number>()
    let cur: string | null = start
    while (cur !== null && !done.has(cur)) {
      const idx = pathIndex.get(cur)
      if (idx !== undefined) {
        for (let i = idx; i < path.length; i += 1) {
          cycleNodes.add(path[i])
        }
        break
      }
      pathIndex.set(cur, path.length)
      path.push(cur)
      cur = rawParentKey(cur)
    }
    for (const k of path) done.add(k)
  }

  const childrenByParent = new Map<string, Session[]>()
  const roots: Session[] = []
  for (const [key, s] of byKey) {
    const pKey = cycleNodes.has(key) ? null : rawParentKey(key)
    if (pKey === null) {
      roots.push(s)
    } else {
      const list = childrenByParent.get(pKey)
      if (list) list.push(s)
      else childrenByParent.set(pKey, [s])
    }
  }

  const buildNode = (s: Session, parentHost: string | null, visited: Set<string>): SessionTreeNode => {
    const key = sessionKey(s)
    const nextVisited = new Set(visited)
    nextVisited.add(key)
    const rawChildren = childrenByParent.get(key) ?? []
    const sortedChildren = [...rawChildren].sort(compareSessions)
    const children: SessionTreeNode[] = []
    for (const child of sortedChildren) {
      if (nextVisited.has(sessionKey(child))) continue
      children.push(buildNode(child, s.last_host, nextVisited))
    }
    return {
      session: s,
      children,
      crossHost: parentHost !== null && s.last_host !== parentHost,
    }
  }

  const countSubtree = (
    node: SessionTreeNode,
    acc: { total: number; active: number; waiting: number; stale: number; conflict: number },
  ) => {
    acc.total += 1
    if (node.session.state !== null) acc.waiting += 1
    if (node.session.stale) acc.stale += 1
    else acc.active += 1
    if (node.session.location_conflicts.length > 0) acc.conflict += 1
    for (const c of node.children) countSubtree(c, acc)
  }

  const byHost = new Map<string, SessionTreeNode[]>()
  for (const root of roots) {
    const node = buildNode(root, null, new Set())
    const list = byHost.get(root.last_host)
    if (list) list.push(node)
    else byHost.set(root.last_host, [node])
  }

  const groups: HostSessionGroup[] = []
  for (const [host, hostRoots] of byHost) {
    hostRoots.sort((a, b) => compareSessions(a.session, b.session))
    const acc = { total: 0, active: 0, waiting: 0, stale: 0, conflict: 0 }
    for (const r of hostRoots) countSubtree(r, acc)
    groups.push({
      host,
      roots: hostRoots,
      totalCount: acc.total,
      activeCount: acc.active,
      waitingCount: acc.waiting,
      staleCount: acc.stale,
      conflictCount: acc.conflict,
    })
  }

  groups.sort((a, b) => {
    const waitDiff = (a.waitingCount > 0 ? 0 : 1) - (b.waitingCount > 0 ? 0 : 1)
    if (waitDiff !== 0) return waitDiff
    const activeDiff = (a.activeCount > 0 ? 0 : 1) - (b.activeCount > 0 ? 0 : 1)
    if (activeDiff !== 0) return activeDiff
    return a.host.localeCompare(b.host)
  })

  return groups
}
