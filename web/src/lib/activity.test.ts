import { describe, expect, it } from 'vitest'
import type { Activity } from '@/api/types'
import { bodyExcerpt, describeChanges, formatValue, summarizeActivity } from './activity'

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
    summary: null,
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
          status: { from: 'backlog', to: 'doing' },
          labels: { from: ['api'], to: ['api', 'cli'] },
        },
      }),
    )
    expect(lines).toEqual([
      { field: 'status', label: '상태', from: '백로그', to: '진행' },
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

describe('상태 변경의 사유', () => {
  const withReason = activity({
    changes: {
      status: { from: 'backlog', to: 'cancelled' },
      status_reason: { from: null, to: 'AGB-20과 중복' },
    },
  })
  it('사유를 상태 줄에 붙이고 따로 줄을 만들지 않는다', () => {
    expect(describeChanges(withReason)).toEqual([{ field: 'status', label: '상태', from: '백로그', to: '취소', reason: 'AGB-20과 중복' }])
  })
  it('다른 필드와 함께 바뀌어도 상태 줄에만 붙는다', () => {
    const lines = describeChanges(
      activity({
        changes: {
          status: { from: 'backlog', to: 'doing' },
          status_reason: { from: null, to: '백로그 → 진행' },
          labels: { from: [], to: ['api'] },
        },
      }),
    )
    expect(lines.map((l) => l.field)).toEqual(['status', 'labels'])
    expect(lines[0].reason).toBe('백로그 → 진행')
    expect(lines[1].reason).toBeUndefined()
  })
  it('상태 변경 없이 사유만 있는 기록은 사유를 별도 줄로 보인다', () => {
    const lines = describeChanges(activity({ changes: { status_reason: { from: null, to: '이유' } } }))
    expect(lines).toEqual([{ field: 'status_reason', label: '상태 사유', from: '없음', to: '이유' }])
  })
  it('긴 사유도 줄에는 전체를 담는다', () => {
    const long = '가'.repeat(200)
    const lines = describeChanges(activity({ changes: { status: { from: 'backlog', to: 'deleted' }, status_reason: { from: null, to: long } } }))
    expect(lines[0].reason).toBe(long)
  })
  it('한 줄 요약에는 사유를 괄호로 붙이고 길면 자른다', () => {
    expect(summarizeActivity(withReason)).toBe('상태: 백로그 → 취소 (사유: AGB-20과 중복)')
    const long = activity({ changes: { status: { from: 'backlog', to: 'deleted' }, status_reason: { from: null, to: '나'.repeat(100) } } })
    expect(summarizeActivity(long)).toBe(`상태: 백로그 → 삭제 (사유: ${'나'.repeat(60)}…)`)
  })
})

describe('확인 표시의 변경', () => {
  it('확인과 해제를 한국어로 보인다', () => {
    expect(formatValue(true, 'acknowledged')).toBe('확인됨')
    expect(formatValue(false, 'acknowledged')).toBe('미확인')
    const lines = describeChanges(activity({ changes: { acknowledged: { from: false, to: true } } }))
    expect(lines).toEqual([{ field: 'acknowledged', label: '확인', from: '미확인', to: '확인됨' }])
    expect(summarizeActivity(activity({ changes: { acknowledged: { from: true, to: false } } }))).toBe('확인: 확인됨 → 미확인')
  })
})

describe('주제 변경', () => {
  const change = activity({ changes: { topic: { from: null, to: 7 } } })
  it('주제 ID를 이름으로 바꾸는 조회가 있으면 경로를 보인다', () => {
    const topicName = (id: number) => (id === 7 ? '이슈 흐름/트리아지' : undefined)
    expect(describeChanges(change, { topicName })).toEqual([{ field: 'topic', label: '주제', from: '없음', to: '이슈 흐름/트리아지' }])
    expect(summarizeActivity(change, { topicName })).toBe('주제: 없음 → 이슈 흐름/트리아지')
  })
  it('이름을 모르면 ID를 보인다', () => {
    expect(describeChanges(change)[0].to).toBe('주제 #7')
    expect(describeChanges(change, { topicName: () => undefined })[0].to).toBe('주제 #7')
  })
})

describe('마일스톤 변경', () => {
  const names: Record<number, string> = { 2: 'v0.2.0', 5: 'v0.3.0' }
  const milestoneName = (id: number) => names[id]
  const change = activity({ changes: { milestone: { from: null, to: 2 } } })
  it('마일스톤 ID를 이름으로 바꾸는 조회가 있으면 이름을 보인다', () => {
    expect(describeChanges(change, { milestoneName })).toEqual([{ field: 'milestone', label: '마일스톤', from: '없음', to: 'v0.2.0' }])
    expect(summarizeActivity(change, { milestoneName })).toBe('마일스톤: 없음 → v0.2.0')
  })
  it('이름을 모르면 ID를 보인다', () => {
    expect(describeChanges(change)[0].to).toBe('마일스톤 #2')
    expect(describeChanges(change, { milestoneName: () => undefined })[0].to).toBe('마일스톤 #2')
    expect(summarizeActivity(change)).toBe('마일스톤: 없음 → 마일스톤 #2')
  })
  it('다른 마일스톤으로 옮기거나 지우면 이전 값과 새 값을 모두 보인다', () => {
    const moved = activity({ changes: { milestone: { from: 2, to: 5 } } })
    const cleared = activity({ changes: { milestone: { from: 5, to: null } } })
    expect(summarizeActivity(moved, { milestoneName })).toBe('마일스톤: v0.2.0 → v0.3.0')
    expect(summarizeActivity(cleared, { milestoneName })).toBe('마일스톤: v0.3.0 → 없음')
  })
  it('주제와 함께 바뀌어도 각자 이름을 찾는다', () => {
    const both = activity({ changes: { topic: { from: null, to: 7 }, milestone: { from: null, to: 2 } } })
    const lines = describeChanges(both, { topicName: () => '이슈 흐름/트리아지', milestoneName })
    expect(lines.map((l) => `${l.label}:${l.to}`)).toEqual(['주제:이슈 흐름/트리아지', '마일스톤:v0.2.0'])
  })
  it('생성 기록은 마일스톤이 있어도 제목 한 줄만 낸다', () => {
    const created = activity({ kind: 'created', changes: { title: { to: '새 이슈' }, milestone: { to: 2 } } })
    expect(describeChanges(created, { milestoneName })).toEqual([{ field: 'title', label: '제목', from: null, to: '새 이슈' }])
  })
})

describe('bodyExcerpt', () => {
  it('20자 이하 본문은 그대로 두고 말줄임표를 붙이지 않는다', () => {
    expect(bodyExcerpt('랭킹 UI 위치를 옮겼다.')).toBe('랭킹 UI 위치를 옮겼다.')
    expect(bodyExcerpt('가'.repeat(20))).toBe('가'.repeat(20))
  })
  it('20자를 넘으면 앞 20자만 남기고 끝에 …를 붙인다', () => {
    expect(bodyExcerpt('가'.repeat(21))).toBe(`${'가'.repeat(20)}…`)
    expect(bodyExcerpt('서버 랭킹 함수를 구현하고 단위 테스트를 추가했다.')).toBe('서버 랭킹 함수를 구현하고 단위 테스…')
  })
  it('공백과 줄바꿈은 한 칸 공백으로 합치고 앞뒤 공백은 뗀다', () => {
    expect(bodyExcerpt('첫 줄\n둘째 줄')).toBe('첫 줄 둘째 줄')
    expect(bodyExcerpt('첫 줄\r\n\r\n  둘째\t\t줄\n')).toBe('첫 줄 둘째 줄')
    expect(bodyExcerpt('\n\n  본문  \n')).toBe('본문')
  })
  it('합친 뒤의 길이로 자른다', () => {
    expect(bodyExcerpt(`${'가'.repeat(20)}\n\n`)).toBe('가'.repeat(20))
    expect(bodyExcerpt(`${'가'.repeat(10)}\n\n\n${'나'.repeat(9)}\n다`)).toBe(`${'가'.repeat(10)} ${'나'.repeat(9)}…`)
  })
  it('코드 포인트 단위로 센다', () => {
    const astral = '\u{1F600}' // UTF-16으로 두 칸을 차지하는 한 글자
    expect(bodyExcerpt(astral.repeat(20))).toBe(astral.repeat(20))
    expect(bodyExcerpt(astral.repeat(21))).toBe(`${astral.repeat(20)}…`)
  })
  it('빈 본문과 공백뿐인 본문은 빈 문자열이다', () => {
    expect(bodyExcerpt('')).toBe('')
    expect(bodyExcerpt(' \n\t ')).toBe('')
  })
})

describe('summarizeActivity', () => {
  it('생성은 제목, 수정은 필드 요약이다', () => {
    expect(summarizeActivity(activity({ kind: 'created', changes: { title: { to: '새 이슈' } } }))).toBe('새 이슈')
    expect(
      summarizeActivity(activity({ changes: { status: { from: 'backlog', to: 'doing' }, body: { from: 'a', to: 'b' } } })),
    ).toBe('상태: 백로그 → 진행 · 본문 수정')
  })
  it('요약이 있는 댓글은 본문과 무관하게 요약이다', () => {
    expect(summarizeActivity(activity({ kind: 'commented', summary: '서버 랭킹 함수 구현', body: '긴 본문\n둘째 줄' }))).toBe('서버 랭킹 함수 구현')
  })
  it('요약이 없는 댓글은 본문 앞부분이다', () => {
    expect(summarizeActivity(activity({ kind: 'commented', body: '첫 줄\n둘째 줄' }))).toBe('첫 줄 둘째 줄')
    expect(summarizeActivity(activity({ kind: 'commented', body: '가'.repeat(25) }))).toBe(`${'가'.repeat(20)}…`)
  })
  it('요약도 본문도 없는 댓글은 빈 문자열이다', () => {
    expect(summarizeActivity(activity({ kind: 'commented' }))).toBe('')
  })
  it('댓글이 아닌 기록은 summary가 있어도 쓰지 않는다', () => {
    expect(summarizeActivity(activity({ kind: 'created', summary: '무시', changes: { title: { to: '새 이슈' } } }))).toBe('새 이슈')
  })
})
