import { describe, expect, it } from 'vitest'
import type { Milestone } from '@/api/types'
import {
  MILESTONE_VIEWS,
  formFromMilestone,
  milestoneCreateBody,
  milestoneIssuesPath,
  milestoneListParams,
  milestoneNameMap,
  milestonePatchBody,
  parseMilestoneView,
  progressLabel,
  progressPercent,
  validateMilestoneForm,
  viewStatuses,
} from './milestones'

function milestone(partial: Partial<Milestone> = {}): Milestone {
  return {
    id: 2,
    project: 'AGB',
    name: 'v0.2.0',
    description: '운영 안정화',
    target_date: '2026-11-01',
    status: 'open',
    progress: { total: 5, done: 2 },
    closed_at: null,
    created_at: '2026-10-18T09:00:00.000Z',
    updated_at: '2026-10-18T09:00:00.000Z',
    created_by: 'owner',
    updated_by: 'owner',
    ...partial,
  }
}

describe('progressPercent', () => {
  it('완료 비율을 정수 퍼센트로 반올림한다', () => {
    expect(progressPercent({ total: 5, done: 2 })).toBe(40)
    expect(progressPercent({ total: 3, done: 1 })).toBe(33)
    expect(progressPercent({ total: 3, done: 2 })).toBe(67)
  })
  it('대상 이슈가 없으면 0이다', () => {
    expect(progressPercent({ total: 0, done: 0 })).toBe(0)
  })
  it('하나도 끝나지 않았으면 0, 모두 끝났으면 100이다', () => {
    expect(progressPercent({ total: 5, done: 0 })).toBe(0)
    expect(progressPercent({ total: 5, done: 5 })).toBe(100)
    expect(progressPercent({ total: 1, done: 1 })).toBe(100)
  })
  it('다 끝나지 않았는데 100으로 올림하지 않는다', () => {
    expect(progressPercent({ total: 200, done: 199 })).toBe(99)
    expect(progressPercent({ total: 1000, done: 999 })).toBe(99)
  })
  it('하나라도 끝났는데 0으로 내림하지 않는다', () => {
    expect(progressPercent({ total: 1000, done: 1 })).toBe(1)
    expect(progressPercent({ total: 500, done: 1 })).toBe(1)
  })
})

describe('progressLabel', () => {
  it('완료/전체와 퍼센트를 보인다', () => {
    expect(progressLabel({ total: 5, done: 2 })).toBe('2/5 · 40%')
    expect(progressLabel({ total: 3, done: 3 })).toBe('3/3 · 100%')
    expect(progressLabel({ total: 4, done: 0 })).toBe('0/4 · 0%')
  })
  it('대상 이슈가 없으면 0/0 대신 이슈 없음이다', () => {
    expect(progressLabel({ total: 0, done: 0 })).toBe('이슈 없음')
  })
})

describe('보기 전환', () => {
  it('열림, 닫힘, 전체 순서로 세 보기를 둔다', () => {
    expect(MILESTONE_VIEWS.map((v) => `${v.value}:${v.label}`)).toEqual(['open:열림', 'closed:닫힘', 'all:전체'])
  })
  it('주소의 status가 없거나 모르는 값이면 열림이다', () => {
    expect(parseMilestoneView(null)).toBe('open')
    expect(parseMilestoneView('')).toBe('open')
    expect(parseMilestoneView('open')).toBe('open')
    expect(parseMilestoneView('deleted')).toBe('open')
    expect(parseMilestoneView('bogus')).toBe('open')
  })
  it('주소의 closed와 all은 그대로 읽는다', () => {
    expect(parseMilestoneView('closed')).toBe('closed')
    expect(parseMilestoneView('all')).toBe('all')
  })
  it('전체는 삭제된 마일스톤까지 포함한다', () => {
    expect(viewStatuses('open')).toEqual(['open'])
    expect(viewStatuses('closed')).toEqual(['closed'])
    expect(viewStatuses('all')).toEqual(['open', 'closed', 'deleted'])
  })
})

describe('milestoneListParams', () => {
  it('열린 것만이면 서버 기본값이므로 status를 보내지 않는다', () => {
    expect(milestoneListParams(['open'])).toEqual({})
    expect(milestoneListParams(['open', 'open'])).toEqual({})
    expect(milestoneListParams([])).toEqual({})
  })
  it('하나만 고르면 그 상태를 보낸다', () => {
    expect(milestoneListParams(['closed'])).toEqual({ status: 'closed' })
    expect(milestoneListParams(['deleted'])).toEqual({ status: 'deleted' })
  })
  it('여럿이면 열림, 닫힘, 삭제 순으로 쉼표로 잇는다', () => {
    expect(milestoneListParams(['closed', 'open'])).toEqual({ status: 'open,closed' })
    expect(milestoneListParams(['deleted', 'open'])).toEqual({ status: 'open,deleted' })
  })
  it('세 상태를 모두 고르면 all이다', () => {
    expect(milestoneListParams(['open', 'closed', 'deleted'])).toEqual({ status: 'all' })
    expect(milestoneListParams(['deleted', 'closed', 'open'])).toEqual({ status: 'all' })
  })
})

describe('milestoneIssuesPath', () => {
  it('이슈 목록의 milestone 필터 주소다', () => {
    expect(milestoneIssuesPath(7)).toBe('/issues?milestone=7')
  })
})

describe('milestoneNameMap', () => {
  it('ID로 이름을 찾는다', () => {
    const names = milestoneNameMap([milestone({ id: 2, name: 'v0.2.0' }), milestone({ id: 5, name: 'v0.3.0' })])
    expect(names.get(2)).toBe('v0.2.0')
    expect(names.get(5)).toBe('v0.3.0')
    expect(names.get(9)).toBeUndefined()
  })
  it('빈 목록은 빈 표다', () => {
    expect(milestoneNameMap([]).size).toBe(0)
  })
})

describe('validateMilestoneForm', () => {
  const ok = { name: 'v0.2.0', description: '', targetDate: '' }
  it('이름만 있어도 통과한다', () => {
    expect(validateMilestoneForm(ok)).toEqual({})
  })
  it('이름이 비었거나 공백뿐이면 거절한다', () => {
    expect(validateMilestoneForm({ ...ok, name: '' }).name).toBe('1~50자여야 합니다.')
    expect(validateMilestoneForm({ ...ok, name: '   ' }).name).toBe('1~50자여야 합니다.')
  })
  it('이름은 앞뒤 공백을 뗀 뒤 50자까지 허용한다', () => {
    expect(validateMilestoneForm({ ...ok, name: 'a'.repeat(50) })).toEqual({})
    expect(validateMilestoneForm({ ...ok, name: `  ${'a'.repeat(50)}  ` })).toEqual({})
    expect(validateMilestoneForm({ ...ok, name: 'a'.repeat(51) }).name).toBe('1~50자여야 합니다.')
  })
  it('글자 수는 코드 포인트 단위로 센다', () => {
    const astral = '\u{1F600}' // UTF-16으로 두 칸을 차지하는 한 글자
    expect(validateMilestoneForm({ ...ok, name: astral.repeat(50) })).toEqual({})
    expect(validateMilestoneForm({ ...ok, name: astral.repeat(51) }).name).toBeDefined()
  })
  it('설명은 2000자까지 허용한다', () => {
    expect(validateMilestoneForm({ ...ok, description: '가'.repeat(2000) })).toEqual({})
    expect(validateMilestoneForm({ ...ok, description: '가'.repeat(2001) }).description).toBe('2000자 이하여야 합니다.')
  })
  it('목표일은 비었거나 실제 있는 YYYY-MM-DD여야 한다', () => {
    expect(validateMilestoneForm({ ...ok, targetDate: '2026-11-01' })).toEqual({})
    expect(validateMilestoneForm({ ...ok, targetDate: '2028-02-29' })).toEqual({})
    for (const bad of ['2026-02-30', '2026-13-01', '2026-1-1', '11/01/2026', 'abc', '2026-11-01T00:00']) {
      expect(validateMilestoneForm({ ...ok, targetDate: bad }).target_date, bad).toBe('YYYY-MM-DD 형식의 날짜여야 합니다.')
    }
  })
  it('위반이 여럿이면 한꺼번에 돌려준다', () => {
    const errors = validateMilestoneForm({ name: '', description: '가'.repeat(2001), targetDate: 'x' })
    expect(Object.keys(errors).sort()).toEqual(['description', 'name', 'target_date'])
  })
})

describe('formFromMilestone', () => {
  it('마일스톤의 값을 폼 값으로 옮기고 목표일이 없으면 빈 문자열이다', () => {
    expect(formFromMilestone(milestone())).toEqual({ name: 'v0.2.0', description: '운영 안정화', targetDate: '2026-11-01' })
    expect(formFromMilestone(milestone({ target_date: null }))).toEqual({ name: 'v0.2.0', description: '운영 안정화', targetDate: '' })
  })
  it('새 마일스톤이면 모두 비어 있다', () => {
    expect(formFromMilestone()).toEqual({ name: '', description: '', targetDate: '' })
  })
})

describe('milestoneCreateBody', () => {
  it('이름만 보내고 비어 있는 설명과 목표일은 보내지 않는다', () => {
    expect(milestoneCreateBody({ name: '  v0.2.0 ', description: '', targetDate: '' })).toEqual({ name: 'v0.2.0' })
    expect(milestoneCreateBody({ name: 'v1', description: '  \n ', targetDate: '' })).toEqual({ name: 'v1' })
  })
  it('설명과 목표일이 있으면 함께 보낸다', () => {
    expect(milestoneCreateBody({ name: 'v1', description: ' 설명 ', targetDate: '2026-11-01' })).toEqual({
      name: 'v1',
      description: '설명',
      target_date: '2026-11-01',
    })
  })
})

describe('milestonePatchBody', () => {
  const current = milestone()
  const same = formFromMilestone(current)
  it('바뀐 것이 없으면 빈 본문이다', () => {
    expect(milestonePatchBody(current, same)).toEqual({})
  })
  it('앞뒤 공백만 바뀌었으면 바뀐 것으로 보지 않는다', () => {
    expect(milestonePatchBody(current, { ...same, name: ` ${current.name} `, description: `${current.description}\n` })).toEqual({})
  })
  it('저장된 값에 공백이 있어도 건드리지 않았으면 그대로 둔다', () => {
    const padded = milestone({ name: ' v0.2.0', description: '설명\n' })
    expect(milestonePatchBody(padded, formFromMilestone(padded))).toEqual({})
  })
  it('바뀐 필드만 담는다', () => {
    expect(milestonePatchBody(current, { ...same, name: 'v0.2.1' })).toEqual({ name: 'v0.2.1' })
    expect(milestonePatchBody(current, { ...same, description: '새 설명' })).toEqual({ description: '새 설명' })
    expect(milestonePatchBody(current, { ...same, targetDate: '2026-12-01' })).toEqual({ target_date: '2026-12-01' })
  })
  it('설명을 비우면 빈 문자열을, 목표일을 비우면 null을 보낸다', () => {
    expect(milestonePatchBody(current, { ...same, description: '' })).toEqual({ description: '' })
    expect(milestonePatchBody(current, { ...same, targetDate: '' })).toEqual({ target_date: null })
  })
  it('목표일이 없던 마일스톤에 날짜를 정하면 그 날짜를 보낸다', () => {
    const none = milestone({ target_date: null })
    expect(milestonePatchBody(none, { ...formFromMilestone(none), targetDate: '2026-11-01' })).toEqual({ target_date: '2026-11-01' })
    expect(milestonePatchBody(none, formFromMilestone(none))).toEqual({})
  })
  it('여러 필드를 함께 바꿀 수 있다', () => {
    expect(milestonePatchBody(current, { name: 'v9', description: '', targetDate: '' })).toEqual({ name: 'v9', description: '', target_date: null })
  })
})
