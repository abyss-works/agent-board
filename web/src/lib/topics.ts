import type { Topic } from '@/api/types'

/** 대주제 하나와 그 아래 주제들 */
export interface TopicGroup {
  top: Topic
  children: Topic[]
}

function byName(a: Topic, b: Topic): number {
  if (a.name !== b.name) return a.name < b.name ? -1 : 1
  return a.id - b.id
}

/**
 * 주제 목록을 대주제별로 묶는다. 대주제와 그 아래 주제는 각각 이름순이다.
 * 상위가 목록에 없는 주제(예: 삭제된 주제만 조회한 경우)는 대주제처럼 따로 둔다.
 */
export function groupTopics(items: readonly Topic[]): TopicGroup[] {
  const ids = new Set(items.map((t) => t.id))
  const tops = items.filter((t) => t.parent === null || !ids.has(t.parent)).sort(byName)
  return tops.map((top) => ({
    top,
    children: items.filter((t) => t.parent === top.id).sort(byName),
  }))
}

export interface FlatTopic {
  topic: Topic
  /** 0은 대주제, 1은 그 아래 주제 */
  depth: 0 | 1
}

/** 선택 목록에 그릴 순서: 대주제 다음에 그 아래 주제. */
export function flattenTopics(groups: readonly TopicGroup[]): FlatTopic[] {
  return groups.flatMap((g) => [{ topic: g.top, depth: 0 as const }, ...g.children.map((c) => ({ topic: c, depth: 1 as const }))])
}

/** 선택 목록의 항목 이름. 들여쓰기는 그리는 쪽이 깊이로 정한다. */
export function topicOptionLabel(f: FlatTopic): string {
  return f.topic.name
}

/** 주제 ID에서 경로("대주제/주제")를 찾는 표 */
export function topicNameMap(items: readonly Topic[]): Map<number, string> {
  return new Map(items.map((t) => [t.id, t.path]))
}

/** 활성 하위 주제가 없는 대주제나 하위 주제만 지울 수 있다. 서버도 하위 주제가 있으면 409로 거절한다. */
export function canDeleteTopic(group: TopicGroup): boolean {
  return group.children.length === 0
}
