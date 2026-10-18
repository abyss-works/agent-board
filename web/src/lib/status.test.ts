import { describe, expect, it } from 'vitest'
import { ISSUE_STATUSES } from '@/api/types'
import { MAX_REASON, reasonError, reasonRequired, statusChangePatch } from './status'

describe('reasonRequired', () => {
  it('취소와 삭제로 바꿀 때만 사유가 필요하다', () => {
    const required = ISSUE_STATUSES.filter(reasonRequired)
    expect(required).toEqual(['cancelled', 'deleted'])
  })
})

describe('reasonError', () => {
  it('앞뒤 공백을 뗀 뒤 1~500자면 통과한다', () => {
    expect(reasonError('중복')).toBeNull()
    expect(reasonError('  중복  ')).toBeNull()
    expect(reasonError('가'.repeat(MAX_REASON))).toBeNull()
  })
  it('비었거나 공백뿐이면 거절한다', () => {
    expect(reasonError('')).not.toBeNull()
    expect(reasonError('  \n ')).not.toBeNull()
  })
  it('500자를 넘으면 거절한다', () => {
    expect(reasonError('가'.repeat(MAX_REASON + 1))).not.toBeNull()
  })
  it('글자 수를 코드 포인트로 센다', () => {
    expect(reasonError('\u{1F600}'.repeat(MAX_REASON))).toBeNull()
    expect(reasonError('\u{1F600}'.repeat(MAX_REASON + 1))).not.toBeNull()
  })
})

describe('statusChangePatch', () => {
  it('사유가 있으면 앞뒤 공백을 떼어 status_reason으로 함께 보낸다', () => {
    expect(statusChangePatch('cancelled', '  AGB-20과 중복 ')).toEqual({ status: 'cancelled', status_reason: 'AGB-20과 중복' })
  })
  it('사유가 없으면 상태만 보내 서버의 기본 메시지를 쓰게 한다', () => {
    expect(statusChangePatch('doing')).toEqual({ status: 'doing' })
    expect(statusChangePatch('doing', '   ')).toEqual({ status: 'doing' })
  })
})
