import type { IssueCreate, IssuePatch } from '@/api/types'

/** 이슈 생성 요청에 붙일 결정 필요. 비었으면 아무것도 붙이지 않아 서버가 값 없음으로 둔다. */
export function decisionForCreate(text: string): Pick<IssueCreate, 'decision_needed'> {
  const trimmed = text.trim()
  return trimmed ? { decision_needed: trimmed } : {}
}

/** 이슈 상세에서 고친 결정 필요의 PATCH 본문. 비우면 null을 보내 지운다. 서버는 빈 문자열을 받지 않는다. */
export function decisionPatch(text: string): IssuePatch {
  return { decision_needed: text.trim() || null }
}
