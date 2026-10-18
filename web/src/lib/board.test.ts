import { describe, expect, it } from 'vitest'
import type { Board, IssueSummary } from '@/api/types'
import { BOARD_STATUSES, boardColumns, moveIssue } from './board'

function issue(key: string, status: IssueSummary['status']): IssueSummary {
  return {
    key,
    project: 'AGB',
    number: Number(key.split('-')[1]),
    title: key,
    type: 'feature',
    status,
    priority: 'P2',
    labels: [],
    parent: null,
    depends_on: [],
    refs: [],
    recheck_condition: null,
    topic: null,
    topic_path: null,
    milestone: null,
    milestone_name: null,
    status_reason: null,
    acknowledged_at: null,
    acknowledged_by: null,
    version: 1,
    created_at: '2026-10-17T00:00:00.000Z',
    updated_at: '2026-10-17T00:00:00.000Z',
    closed_at: null,
    status_changed_at: '2026-10-17T00:00:00.000Z',
    status_changed_by: 'owner',
    created_by: 'owner',
    updated_by: 'owner',
  }
}

const board: Board = {
  project: 'AGB',
  columns: [
    { status: 'backlog', total: 1, issues: [issue('AGB-1', 'backlog')] },
    { status: 'todo', total: 0, issues: [] },
    { status: 'doing', total: 1, issues: [issue('AGB-2', 'doing')] },
    { status: 'review', total: 0, issues: [] },
    { status: 'done', total: 0, issues: [] },
    { status: 'cancelled', total: 1, issues: [issue('AGB-3', 'cancelled')] },
    { status: 'deleted', total: 1, issues: [issue('AGB-4', 'deleted')] },
  ],
}

describe('boardColumns', () => {
  it('기본은 다섯 열이고 순서가 고정이다', () => {
    const cols = boardColumns(board, false)
    expect(cols.map((c) => c.status)).toEqual([...BOARD_STATUSES])
    expect(cols[0].issues.map((i) => i.key)).toEqual(['AGB-1'])
  })
  it('토글하면 취소 열을 더하고 삭제 열은 넣지 않는다', () => {
    const cols = boardColumns(board, true)
    expect(cols.map((c) => c.status)).toEqual([...BOARD_STATUSES, 'cancelled'])
    expect(cols.at(-1)?.issues.map((i) => i.key)).toEqual(['AGB-3'])
  })
  it('응답에 없는 상태의 열은 빈 열로 채운다', () => {
    const cols = boardColumns({ project: null, columns: [] }, true)
    expect(cols).toHaveLength(6)
    expect(cols.every((c) => c.total === 0 && c.issues.length === 0)).toBe(true)
  })
})

describe('moveIssue', () => {
  it('카드를 다른 열로 옮기고 상태를 바꾼다', () => {
    const next = moveIssue(board, 'AGB-1', 'doing')
    const backlog = next.columns.find((c) => c.status === 'backlog')!
    const doing = next.columns.find((c) => c.status === 'doing')!
    expect(backlog.issues).toHaveLength(0)
    expect(backlog.total).toBe(0)
    expect(doing.issues.map((i) => i.key)).toEqual(['AGB-2', 'AGB-1'])
    expect(doing.total).toBe(2)
    expect(doing.issues[1].status).toBe('doing')
  })
  it('원본을 바꾸지 않는다', () => {
    moveIssue(board, 'AGB-1', 'doing')
    expect(board.columns[0].issues).toHaveLength(1)
  })
  it('없는 키면 그대로 돌려준다', () => {
    expect(moveIssue(board, 'AGB-99', 'doing')).toBe(board)
  })
})
