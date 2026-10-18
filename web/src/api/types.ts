// agent-board REST API 응답 타입. 기준은 spec/api.md다.

export type IssueType = 'feature' | 'bug' | 'design' | 'decision' | 'idea' | 'observation'
export type IssueStatus = 'backlog' | 'todo' | 'doing' | 'review' | 'done' | 'cancelled' | 'deleted'
export type ProjectStatus = 'active' | 'archived' | 'deleted'
export type Priority = 'P0' | 'P1' | 'P2' | 'P3'
export type ActorKind = 'user' | 'agent'
export type ActivityKind = 'created' | 'updated' | 'commented'
export type TopicStatus = 'active' | 'deleted'
export type MilestoneStatus = 'open' | 'closed' | 'deleted'

export const ISSUE_TYPES: IssueType[] = ['feature', 'bug', 'design', 'decision', 'idea', 'observation']
export const ISSUE_STATUSES: IssueStatus[] = ['backlog', 'todo', 'doing', 'review', 'done', 'cancelled', 'deleted']
export const OPEN_STATUSES: IssueStatus[] = ['backlog', 'todo', 'doing', 'review']
export const PRIORITIES: Priority[] = ['P0', 'P1', 'P2', 'P3']
export const PROJECT_STATUSES: ProjectStatus[] = ['active', 'archived', 'deleted']
export const ACTIVITY_KINDS: ActivityKind[] = ['created', 'updated', 'commented']
export const MILESTONE_STATUSES: MilestoneStatus[] = ['open', 'closed', 'deleted']

export interface Project {
  key: string
  name: string
  status: ProjectStatus
  created_at: string
  updated_at: string
  created_by: string
  updated_by: string
}

/** 프로젝트의 두 단계 분류. 상위가 없으면 대주제, 있으면 그 아래 주제다. */
export interface Topic {
  id: number
  project: string
  name: string
  parent: number | null
  /** `대주제/주제` 또는 `대주제` */
  path: string
  status: TopicStatus
  created_at: string
  updated_at: string
  created_by: string
  updated_by: string
}

/** 마일스톤의 진행. 취소·삭제한 이슈를 뺀 이슈 수(total)와 그중 완료한 수(done)다. 조회할 때마다 서버가 계산한다. */
export interface MilestoneProgress {
  total: number
  done: number
}

/** 프로젝트의 출시나 목표 단위로 이슈를 묶는다. */
export interface Milestone {
  id: number
  project: string
  name: string
  description: string
  /** `YYYY-MM-DD`. 정하지 않았으면 null이다. */
  target_date: string | null
  status: MilestoneStatus
  progress: MilestoneProgress
  /** 닫은 시각. 열려 있으면 null이다. */
  closed_at: string | null
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
  /** 이슈에 붙은 주제의 ID. 대주제와 주제 어느 쪽이든 붙일 수 있다. */
  topic: number | null
  /** `대주제/주제` 또는 `대주제`. 주제가 없으면 null이다. */
  topic_path: string | null
  /** 이슈가 속한 마일스톤의 ID. 마일스톤은 하나만 붙는다. */
  milestone: number | null
  /** 마일스톤 이름. 마일스톤이 없으면 null이다. 삭제된 마일스톤이어도 이름이 온다. */
  milestone_name: string | null
  /** 마지막 상태 변경의 사유. 상태를 바꾼 적이 없으면 null이다. */
  status_reason: string | null
  /** 사용자가 이 이슈를 확인한 시각과 행위자. 미확인이면 둘 다 null이다. 작업 흐름과 무관한 표시다. */
  acknowledged_at: string | null
  acknowledged_by: string | null
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

/** 이슈 종류별 본문 템플릿. 에이전트가 만드는 이슈는 서버가 body의 `## 제목`을 순서대로 검사한다. */
export interface Template {
  type: IssueType
  /** 이슈 제목을 쓰는 규칙 */
  title_rule: string
  /** body의 `## ` 제목들 */
  headings: string[]
  body: string
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
  /** 댓글의 한 줄 요약. 요약 없는 댓글과 댓글이 아닌 기록은 null이다. */
  summary: string | null
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
  /** 삭제되지 않은 미확인 이슈. 최근에 만든 것부터. */
  unacknowledged: Section<IssueSummary>
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
  topic?: number | null
  milestone?: number | null
}

/** PATCH /v1/issues/{key} 본문(JSON Merge Patch). null은 지움이다. */
export interface IssuePatch {
  title?: string
  body?: string
  type?: IssueType
  status?: IssueStatus
  /** 상태를 바꿀 때만 쓴다. 취소·삭제로 바꿀 때는 필수다. */
  status_reason?: string
  topic?: number | null
  milestone?: number | null
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

export interface TopicCreate {
  name: string
  parent?: number | null
}

export interface TopicPatch {
  name?: string
  parent?: number | null
  status?: TopicStatus
}

/** POST /v1/projects/{key}/milestones 본문 */
export interface MilestoneCreate {
  name: string
  description?: string
  target_date?: string | null
}

/** PATCH /v1/milestones/{id} 본문(JSON Merge Patch). target_date의 null은 목표일을 지운다. */
export interface MilestonePatch {
  name?: string
  description?: string
  target_date?: string | null
  status?: MilestoneStatus
}
