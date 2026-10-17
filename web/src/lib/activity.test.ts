import { describe, expect, it } from 'vitest'
import type { Activity } from '@/api/types'
import { describeChanges, formatValue, summarizeActivity } from './activity'

function activity(partial: Partial<Activity>): Activity {
  return {
    id: 1,
    issue: 'AGB-12',
    project: 'AGB',
    kind: 'updated',
    actor: 'claude',
    session_id: 's1',
    client_host: 'desk',
    at: '2026-10-17T07:10:21.000Z',
    changes: null,
    body: null,
    ...partial,
  }
}

describe('formatValue', () => {
  it('null과 빈 배열은 없음, 배열은 쉼표로 잇는다', () => {
    expect(formatValue(null)).toBe('없음')
    expect(formatValue(undefined)).toBe('없음')
    expect(formatValue([])).toBe('없음')
    expect(formatValue(['api', 'cli'])).toBe('api, cli')
  })
  it('열거형 값은 한국어 이름으로 바꾼다', () => {
    expect(formatValue('doing', 'status')).toBe('진행')
    expect(formatValue('observation', 'type')).toBe('관측')
    expect(formatValue('P1', 'priority')).toBe('P1')
  })
  it('긴 문자열은 자른다', () => {
    expect(formatValue('a'.repeat(100))).toBe(`${'a'.repeat(60)}…`)
  })
})

describe('describeChanges', () => {
  it('updated는 바뀐 필드마다 from → to 한 줄이다', () => {
    const lines = describeChanges(
      activity({
        changes: {
          status: { from: 'todo', to: 'doing' },
          labels: { from: ['api'], to: ['api', 'cli'] },
        },
      }),
    )
    expect(lines).toEqual([
      { field: 'status', label: '상태', from: '할 일', to: '진행' },
      { field: 'labels', label: '라벨', from: 'api', to: 'api, cli' },
    ])
  })
  it('본문 변경은 값을 펼치지 않는다', () => {
    const lines = describeChanges(activity({ changes: { body: { from: 'old', to: 'new' } } }))
    expect(lines).toEqual([{ field: 'body', label: '본문', from: null, to: null }])
  })
  it('created는 제목 한 줄만 낸다', () => {
    const lines = describeChanges(
      activity({ kind: 'created', changes: { title: { to: '새 이슈' }, status: { to: 'backlog' }, labels: { to: [] } } }),
    )
    expect(lines).toEqual([{ field: 'title', label: '제목', from: null, to: '새 이슈' }])
  })
  it('commented와 changes 없음은 빈 목록이다', () => {
    expect(describeChanges(activity({ kind: 'commented', body: '댓글' }))).toEqual([])
    expect(describeChanges(activity({ changes: null }))).toEqual([])
  })
})

describe('summarizeActivity', () => {
  it('댓글은 첫 줄, 생성은 제목, 수정은 필드 요약이다', () => {
    expect(summarizeActivity(activity({ kind: 'commented', body: '첫 줄\n둘째 줄' }))).toBe('첫 줄')
    expect(summarizeActivity(activity({ kind: 'created', changes: { title: { to: '새 이슈' } } }))).toBe('새 이슈')
    expect(
      summarizeActivity(activity({ changes: { status: { from: 'todo', to: 'doing' }, body: { from: 'a', to: 'b' } } })),
    ).toBe('상태: 할 일 → 진행 · 본문 수정')
  })
})
