import type { IssueSummary } from './types'

/** 시험용 요약 이슈. 바꿀 필드만 넘긴다. */
export function issueSummary(partial: Partial<IssueSummary> = {}): IssueSummary {
  return {
    key: 'AGB-1',
    project: 'AGB',
    number: 1,
    title: '제목',
    type: 'feature',
    status: 'backlog',
    priority: 'P2',
    labels: [],
    parent: null,
    depends_on: [],
    refs: [],
    recheck_condition: null,
    decision_needed: null,
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
    ...partial,
  }
}
