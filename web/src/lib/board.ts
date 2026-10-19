import type { Board, BoardColumn, IssueStatus } from '@/api/types'

/** 보드에 늘 보이는 열. 취소는 토글, 삭제는 보드에 두지 않는다. */
export const BOARD_STATUSES = ['backlog', 'doing', 'review', 'done'] as const satisfies readonly IssueStatus[]

export function boardColumns(board: Board, showCancelled: boolean): BoardColumn[] {
  const wanted: IssueStatus[] = showCancelled ? [...BOARD_STATUSES, 'cancelled'] : [...BOARD_STATUSES]
  return wanted.map((status) => board.columns.find((c) => c.status === status) ?? { status, total: 0, issues: [] })
}

/** 카드를 다른 열로 옮긴 새 보드. 낙관적 갱신에 쓴다. 키가 없으면 원본을 돌려준다. */
export function moveIssue(board: Board, key: string, to: IssueStatus): Board {
  const from = board.columns.find((c) => c.issues.some((i) => i.key === key))
  const moved = from?.issues.find((i) => i.key === key)
  if (!from || !moved || from.status === to) return board
  const columns = board.columns.map((c) => {
    if (c.status === from.status) {
      return { ...c, total: Math.max(0, c.total - 1), issues: c.issues.filter((i) => i.key !== key) }
    }
    if (c.status === to) {
      return { ...c, total: c.total + 1, issues: [...c.issues, { ...moved, status: to }] }
    }
    return c
  })
  if (!columns.some((c) => c.status === to)) columns.push({ status: to, total: 1, issues: [{ ...moved, status: to }] })
  return { ...board, columns }
}
