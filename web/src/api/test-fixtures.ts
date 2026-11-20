import type { Activity, DecisionItem, IssueSummary, Session } from './types'

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
    dormant: false,
    ...partial,
  }
}

/** 시험용 결정 필요 항목. 바꿀 필드만 넘긴다. */
export function decisionItem(partial: Partial<DecisionItem> = {}): DecisionItem {
  return {
    ...issueSummary(partial),
    waiting_sessions: partial.waiting_sessions ?? [],
  }
}

/** 시험용 세션. 바꿀 필드만 넘긴다. */
export function sessionFixture(partial: Partial<Session> = {}): Session {
  return {
    actor: 'agent-a',
    session_id: 'sess-1',
    named: true,
    first_seen_at: '2026-10-17T06:00:00.000Z',
    last_seen_at: '2026-10-17T07:00:00.000Z',
    last_host: 'host-a',
    last_project: 'APP',
    parent: null,
    role: 'main',
    issue: null,
    location: null,
    location_conflicts: [],
    state: null,
    state_reason: null,
    state_at: null,
    stale: false,
    updated_at: '2026-10-17T07:00:00.000Z',
    ...partial,
  }
}

/** 시험용 활동 기록. 바꿀 필드만 넘긴다. */
export function activityFixture(partial: Partial<Activity> = {}): Activity {
  return {
    id: 1,
    issue: 'APP-12',
    project: 'APP',
    kind: 'commented',
    actor: 'agent-a',
    session_id: 'sess-1',
    client_host: 'host-a',
    at: '2026-10-17T07:05:00.000Z',
    changes: null,
    summary: null,
    comment_kind: null,
    body: '작업 진행 상황을 남깁니다.',
    ...partial,
  }
}
