import { describe, expect, it } from 'vitest'
import { STATUS_LABEL, TYPE_LABEL, statusLabel, typeLabel } from './labels'

describe('이름 표', () => {
  it('상태 이름은 서버의 여섯 상태에만 있다', () => {
    expect(STATUS_LABEL).toEqual({ backlog: '백로그', doing: '진행', review: '검토', done: '완료', cancelled: '취소', deleted: '삭제' })
  })
  it('종류 이름은 다섯 종류에만 있다', () => {
    expect(TYPE_LABEL).toEqual({ feature: '기능', bug: '버그', design: '설계', idea: '아이디어', observation: '관측' })
  })
  it('없어진 값은 옛 활동 기록을 읽을 수 있게 옛 이름으로 보인다', () => {
    expect(statusLabel('todo')).toBe('할 일')
    expect(typeLabel('decision')).toBe('결정 대기')
    expect(statusLabel('unknown')).toBe('unknown')
  })
})
