import { describe, expect, it } from 'vitest'
import {
  VERIFICATION_ACTIVE_STATUSES,
  clearVerificationPatch,
  pendingVerificationKeys,
  requestVerificationPatch,
  unverifiedListPath,
  verificationBadgeVariant,
  verificationNotePatch,
} from './verification'

describe('pendingVerificationKeys', () => {
  it('검증 필요(pending) 상태인 이슈의 키만 순서대로 돌려준다', () => {
    const items = [
      { key: 'APP-1', verification_state: 'pending' as const },
      { key: 'APP-2', verification_state: 'none' as const },
      { key: 'APP-3', verification_state: 'verified' as const },
      { key: 'APP-4', verification_state: 'pending' as const },
    ]
    expect(pendingVerificationKeys(items)).toEqual(['APP-1', 'APP-4'])
  })
  it('빈 목록은 빈 배열이다', () => {
    expect(pendingVerificationKeys([])).toEqual([])
  })
})

describe('unverifiedListPath', () => {
  it('취소와 삭제를 뺀 상태에서 검증 필요 이슈를 보는 이슈 목록 주소다', () => {
    expect(VERIFICATION_ACTIVE_STATUSES).toEqual(['backlog', 'doing', 'review', 'done'])
    expect(unverifiedListPath()).toBe('/issues?verification=pending&status=backlog%2Cdoing%2Creview%2Cdone')
  })
  it('이슈 목록 화면이 읽는 매개변수로 되돌려 읽힌다', () => {
    const params = new URL(unverifiedListPath(), 'http://x').searchParams
    expect(params.get('verification')).toBe('pending')
    expect(params.get('status')?.split(',')).toEqual([...VERIFICATION_ACTIVE_STATUSES])
  })
})

describe('verificationBadgeVariant', () => {
  it('none은 null, pending은 destructive, verified는 success다', () => {
    expect(verificationBadgeVariant('none')).toBeNull()
    expect(verificationBadgeVariant('pending')).toBe('destructive')
    expect(verificationBadgeVariant('verified')).toBe('success')
  })
})

describe('검증 PATCH 본문 생성', () => {
  it('검증 필요 지정 시 메모 앞뒤 공백을 정리하고 비면 null로 보낸다', () => {
    expect(requestVerificationPatch('  스테이징 화면 확인  ')).toEqual({
      verification_state: 'pending',
      verification_note: '스테이징 화면 확인',
    })
    expect(requestVerificationPatch('   ')).toEqual({
      verification_state: 'pending',
      verification_note: null,
    })
  })
  it('검증 해제는 verification_state를 none으로, 메모를 null로 보낸다', () => {
    expect(clearVerificationPatch()).toEqual({
      verification_state: 'none',
      verification_note: null,
    })
  })
  it('검증 메모 수정은 공백 정리 후 비면 null로 보낸다', () => {
    expect(verificationNotePatch('  로그인 흐름 확인  ')).toEqual({ verification_note: '로그인 흐름 확인' })
    expect(verificationNotePatch('')).toEqual({ verification_note: null })
  })
})
