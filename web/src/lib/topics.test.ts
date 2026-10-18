import { describe, expect, it } from 'vitest'
import type { Topic } from '@/api/types'
import { canDeleteTopic, flattenTopics, groupTopics, topicNameMap, topicOptionLabel } from './topics'

function topic(id: number, name: string, parent: number | null = null, parentName?: string): Topic {
  return {
    id,
    project: 'AGB',
    name,
    parent,
    path: parent === null ? name : `${parentName ?? '?'}/${name}`,
    status: 'active',
    created_at: '2026-10-18T06:30:00.000Z',
    updated_at: '2026-10-18T06:30:00.000Z',
    created_by: 'owner',
    updated_by: 'owner',
  }
}

const items = [
  topic(3, '인프라'),
  topic(7, '트리아지', 1, '이슈 흐름'),
  topic(1, '이슈 흐름'),
  topic(5, '상태 사유', 1, '이슈 흐름'),
  topic(9, '배포', 3, '인프라'),
]

describe('groupTopics', () => {
  it('대주제를 이름순으로, 그 아래 주제를 이름순으로 묶는다', () => {
    const groups = groupTopics(items)
    expect(groups.map((g) => g.top.name)).toEqual(['이슈 흐름', '인프라'])
    expect(groups[0].children.map((c) => c.name)).toEqual(['상태 사유', '트리아지'])
    expect(groups[1].children.map((c) => c.name)).toEqual(['배포'])
  })
  it('하위 주제가 없는 대주제도 한 묶음이다', () => {
    const groups = groupTopics([topic(1, '이슈 흐름')])
    expect(groups).toHaveLength(1)
    expect(groups[0].children).toEqual([])
  })
  it('상위가 목록에 없는 주제는 대주제처럼 따로 둔다', () => {
    const groups = groupTopics([topic(7, '트리아지', 1, '이슈 흐름'), topic(3, '인프라')])
    expect(groups.map((g) => g.top.name)).toEqual(['인프라', '트리아지'])
  })
  it('빈 목록은 빈 배열이고 입력을 바꾸지 않는다', () => {
    expect(groupTopics([])).toEqual([])
    const copy = [...items]
    groupTopics(items)
    expect(items).toEqual(copy)
  })
})

describe('flattenTopics', () => {
  it('대주제 다음에 그 아래 주제가 오고 깊이를 표시한다', () => {
    const flat = flattenTopics(groupTopics(items))
    expect(flat.map((f) => `${f.depth}:${f.topic.name}`)).toEqual(['0:이슈 흐름', '1:상태 사유', '1:트리아지', '0:인프라', '1:배포'])
  })
})

describe('topicNameMap과 topicOptionLabel', () => {
  it('ID로 경로를 찾는다', () => {
    const names = topicNameMap(items)
    expect(names.get(7)).toBe('이슈 흐름/트리아지')
    expect(names.get(3)).toBe('인프라')
    expect(names.get(99)).toBeUndefined()
  })
  it('선택 목록에서 하위 주제는 이름만, 대주제는 이름 그대로 보인다', () => {
    const flat = flattenTopics(groupTopics(items))
    expect(flat.map(topicOptionLabel)).toEqual(['이슈 흐름', '상태 사유', '트리아지', '인프라', '배포'])
  })
})

describe('canDeleteTopic', () => {
  it('활성 하위 주제가 있는 대주제는 지울 수 없다', () => {
    const [flow, infra] = groupTopics(items)
    expect(canDeleteTopic(flow)).toBe(false)
    expect(canDeleteTopic(infra)).toBe(false)
    expect(canDeleteTopic({ top: topic(4, '빈 대주제'), children: [] })).toBe(true)
  })
})
