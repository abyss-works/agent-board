import type { ContextItem, ContextNeighbor, EdgeCandidate, StageMilestone, StageStatus } from '@/api/context-types'

export const STAGES: ReadonlyArray<{ value: StageStatus; label: string; next: StageStatus | null; nextLabel: string | null }> = [
  { value: 'planned', label: '계획', next: 'active', nextLabel: '진행 시작' },
  { value: 'active', label: '진행', next: 'closed', nextLabel: '닫기' },
  { value: 'closed', label: '완료', next: null, nextLabel: null },
]

/** 마일스톤을 3단계 열로 나눈다. 삭제된 것은 뺀다. */
export function groupByStage(items: readonly StageMilestone[]): Record<StageStatus, StageMilestone[]> {
  const out: Record<StageStatus, StageMilestone[]> = { planned: [], active: [], closed: [] }
  for (const m of items) {
    if (m.status === 'planned' || m.status === 'active' || m.status === 'closed') out[m.status].push(m)
  }
  return out
}

export interface NeighborGroups {
  outbound: ContextNeighbor[]
  inbound: ContextNeighbor[]
}

/** 이웃을 방향별로 나눈다. 같은 노드가 두 방향에 있으면 각각 보인다. */
export function splitNeighbors(neighbors: readonly ContextNeighbor[]): NeighborGroups {
  return {
    outbound: neighbors.filter((n) => n.direction === 'outbound'),
    inbound: neighbors.filter((n) => n.direction === 'inbound'),
  }
}

export const EDGE_KIND_LABEL: Record<string, string> = {
  supersedes: '대체',
  about: '관련 이슈',
  depends_on: '선행',
  related: '연관',
}

/** 항목의 점수를 0~100 정수 퍼센트로 줄인다. 범위 밖과 NaN은 끝값으로 맞춘다. */
export function scorePercent(score: number): number {
  if (!Number.isFinite(score)) return 0
  return Math.round(Math.min(1, Math.max(0, score)) * 100)
}

/** 항목 노드가 어느 화면으로 가는지. 이슈와 스펙만 화면이 있고 나머지는 null이다. */
export function itemPath(item: Pick<ContextItem, 'node_type' | 'project' | 'key_or_name'>): string | null {
  switch (item.node_type) {
    case 'issue':
      return `/issues/${encodeURIComponent(item.key_or_name)}`
    case 'spec':
      return `/specs/${encodeURIComponent(item.project)}/${encodeURIComponent(item.key_or_name)}`
    case 'record':
      return `/documents/${encodeURIComponent(item.project)}/${encodeURIComponent(item.key_or_name)}`
    default:
      return null
  }
}

/** 대체(supersedes) 후보를 앞에 세우고 같은 종류 안에서는 확신도 높은 순으로 줄 세운다. */
export function sortCandidates(items: readonly EdgeCandidate[]): EdgeCandidate[] {
  return [...items].sort((a, b) => {
    const ka = a.kind === 'supersedes' ? 0 : 1
    const kb = b.kind === 'supersedes' ? 0 : 1
    if (ka !== kb) return ka - kb
    return (b.confidence ?? 0) - (a.confidence ?? 0) || a.id - b.id
  })
}

export function candidateTarget(c: Pick<EdgeCandidate, 'target_key_or_name' | 'unresolved_target_text'>): string {
  return c.target_key_or_name || c.unresolved_target_text || '-'
}
