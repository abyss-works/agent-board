import { ISSUE_STATUSES, type IssuePatch, type IssueStatus, type IssueSummary, type VerificationState } from '@/api/types'

/** 현황의 검증 필요 절이 세는 상태. 취소와 삭제된 이슈는 뺀다(완료는 포함한다). */
export const VERIFICATION_ACTIVE_STATUSES: IssueStatus[] = ISSUE_STATUSES.filter((s) => s !== 'cancelled' && s !== 'deleted')

/** 이슈 목록에서 검증 필요(pending) 상태인 이슈의 키. 일괄 검증 완료가 이 키마다 요청을 보낸다. */
export function pendingVerificationKeys(items: ReadonlyArray<Pick<IssueSummary, 'key' | 'verification_state'>>): string[] {
  return items.filter((i) => i.verification_state === 'pending').map((i) => i.key)
}

/** 현황의 검증 필요 칸에서 이어지는 이슈 목록 주소. 칸과 같은 범위(취소·삭제 제외 모든 상태)를 보여 준다. */
export function unverifiedListPath(): string {
  const params = new URLSearchParams({ verification: 'pending', status: VERIFICATION_ACTIVE_STATUSES.join(',') })
  return `/issues?${params.toString()}`
}

/** 검증 상태별 배지 강조 종류. none은 배지를 그리지 않는다. */
export function verificationBadgeVariant(state: VerificationState): 'destructive' | 'success' | null {
  if (state === 'pending') return 'destructive'
  if (state === 'verified') return 'success'
  return null
}

/** 이슈를 검증 필요(pending)로 지정하는 PATCH 본문. */
export function requestVerificationPatch(note: string): IssuePatch {
  const trimmed = note.trim()
  return {
    verification_state: 'pending',
    verification_note: trimmed || null,
  }
}

/** 이슈의 검증 상태를 해당 없음(none)으로 해제하는 PATCH 본문. */
export function clearVerificationPatch(): IssuePatch {
  return {
    verification_state: 'none',
    verification_note: null,
  }
}

/** 검증 메모만 수정하는 PATCH 본문. 비우면 null이다. */
export function verificationNotePatch(note: string): IssuePatch {
  const trimmed = note.trim()
  return {
    verification_note: trimmed || null,
  }
}
