import type { IssuePatch, IssueStatus } from '@/api/types'

/** 상태 변경 사유의 최대 글자 수. 서버 규칙(1~500자)과 같다. */
export const MAX_REASON = 500

/** 이 상태로 바꿀 때 사유를 꼭 받아야 하는지. 취소와 삭제만 해당한다. */
export function reasonRequired(status: IssueStatus): boolean {
  return status === 'cancelled' || status === 'deleted'
}

/** 사유 입력의 문제를 한 문장으로. 입력이 괜찮으면 null이다. 앞뒤 공백은 뗀 뒤 센다. */
export function reasonError(text: string): string | null {
  const length = Array.from(text.trim()).length
  if (length < 1) return '사유를 입력하세요.'
  if (length > MAX_REASON) return `${MAX_REASON}자 이하여야 합니다.`
  return null
}

/** 상태를 바꾸는 PATCH 본문. 사유가 비어 있으면 보내지 않아 서버가 기본 메시지를 적게 한다. */
export function statusChangePatch(status: IssueStatus, reason?: string): IssuePatch {
  const trimmed = reason?.trim()
  return trimmed ? { status, status_reason: trimmed } : { status }
}
