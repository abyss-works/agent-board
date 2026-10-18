import { ISSUE_STATUSES, type IssueStatus, type IssueSummary } from '@/api/types'

/** 현황의 미확인 절이 세는 상태. 삭제된 이슈는 뺀다. */
export const UNACKNOWLEDGED_STATUSES: IssueStatus[] = ISSUE_STATUSES.filter((s) => s !== 'deleted')

/** 이슈 목록에서 아직 확인하지 않은 이슈의 키. 목록 모두 확인이 이 키마다 요청을 보낸다. */
export function unacknowledgedKeys(items: ReadonlyArray<Pick<IssueSummary, 'key' | 'acknowledged_at'>>): string[] {
  return items.filter((i) => i.acknowledged_at === null).map((i) => i.key)
}

/** 현황의 미확인 칸에서 이어지는 이슈 목록 주소. 칸과 같은 범위(삭제 제외 모든 상태)를 보여 준다. */
export function unacknowledgedListPath(): string {
  const params = new URLSearchParams({ acknowledged: 'false', status: UNACKNOWLEDGED_STATUSES.join(',') })
  return `/issues?${params.toString()}`
}
