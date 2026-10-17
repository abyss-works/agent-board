// agent-board REST API 응답 타입. 기준은 spec/api.md다.

export type IssueType = 'feature' | 'bug' | 'design' | 'decision' | 'idea' | 'observation'
export type IssueStatus = 'backlog' | 'todo' | 'doing' | 'review' | 'done' | 'cancelled' | 'deleted'
export type ProjectStatus = 'active' | 'archived' | 'deleted'
export type Priority = 'P0' | 'P1' | 'P2' | 'P3'
export type ActorKind = 'user' | 'agent'
export type ActivityKind = 'created' | 'updated' | 'commented'

export const ISSUE_TYPES: IssueType[] = ['feature', 'bug', 'design', 'decision', 'idea', 'observation']
export const ISSUE_STATUSES: IssueStatus[] = ['backlog', 'todo', 'doing', 'review', 'done', 'cancelled', 'deleted']
export const OPEN_STATUSES: IssueStatus[] = ['backlog', 'todo', 'doing', 'review']
export const PRIORITIES: Priority[] = ['P0', 'P1', 'P2', 'P3']
export const PROJECT_STATUSES: ProjectStatus[] = ['active', 'archived', 'deleted']
export const ACTIVITY_KINDS: ActivityKind[] = ['created', 'updated', 'commented']

export interface Project {
  key: string
  name: string
  status: ProjectStatus
  created_at: string
  updated_at: string
  created_by: string
  updated_by: string
}

/** 목록, 보드, 큐, 현황에 담기는 요약 이슈. 본문이 없다. */
export interface IssueSummary {
  key: string
  project: string
  number: number
  title: string
  type: IssueType
  status: IssueStatus
  priority: Priority
  labels: string[]
  parent: string | null
  depends_on: string[]
  refs: string[]
  recheck_condition: string | null
  version: number
  created_at: string
  updated_at: string
  closed_at: string | null
  status_changed_at: string
  status_changed_by: string
  created_by: string
  updated_by: string
  /** q 검색일 때만 붙는다. 일치 부분은 [ ]로 감싼다. */
  snippet?: string
}

export interface Issue extends IssueSummary {
  body: string
}

export interface QueueItem extends IssueSummary {
  ready: boolean
  blocked_by: string[]
}

export interface Change {
  from?: unknown
  to?: unknown
}

export interface Activity {
  id: number
  issue: string
  project: string
  kind: ActivityKind
  actor: string
  session_id: string
  client_host: string
  at: string
  changes: Record<string, Change> | null
  body: string | null
}

export interface Actor {
  id: string
  kind: ActorKind
}

export interface Page<T> {
  items: T[]
  total: number
  limit: number
  offset: number
}

export interface Section<T> {
  total: number
  items: T[]
}

export interface BoardColumn {
  status: IssueStatus
  total: number
  issues: IssueSummary[]
}

export interface Board {
  project: string | null
  columns: BoardColumn[]
}

export interface Overview {
  project: Project
  generated_at: string
  counts: {
    total: number
    open: number
    by_status: Record<IssueStatus, number>
    by_type: Record<IssueType, number>
    by_priority: Record<Priority, number>
  }
  doing: Section<IssueSummary>
  review: Section<IssueSummary>
  blocked: Section<QueueItem>
  decisions: Section<IssueSummary>
  next: Section<QueueItem>
  recent_activity: Section<Activity>
}

export interface LabelCount {
  label: string
  count: number
}

export interface Health {
  status: string
  version: string
  api_version: number
}

export interface ErrorDetail {
  field?: string
  reason?: string
  message?: string
}

/** POST /v1/issues 본문 */
export interface IssueCreate {
  project: string
  title: string
  body?: string
  type?: IssueType
  status?: IssueStatus
  priority?: Priority
  labels?: string[]
  parent?: string | null
  depends_on?: string[]
  refs?: string[]
  recheck_condition?: string | null
}

/** PATCH /v1/issues/{key} 본문(JSON Merge Patch). null은 지움이다. */
export interface IssuePatch {
  title?: string
  body?: string
  type?: IssueType
  status?: IssueStatus
  priority?: Priority
  labels?: string[]
  parent?: string | null
  depends_on?: string[]
  refs?: string[]
  recheck_condition?: string | null
}

export interface ProjectCreate {
  key: string
  name: string
}

export interface ProjectPatch {
  name?: string
  status?: ProjectStatus
}
