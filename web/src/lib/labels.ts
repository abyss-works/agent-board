import type { ActivityKind, IssueStatus, IssueType, MilestoneStatus, Priority, ProjectStatus } from '@/api/types'

export const STATUS_LABEL: Record<IssueStatus, string> = {
  backlog: '백로그',
  todo: '할 일',
  doing: '진행',
  review: '검토',
  done: '완료',
  cancelled: '취소',
  deleted: '삭제',
}

export const TYPE_LABEL: Record<IssueType, string> = {
  feature: '기능',
  bug: '버그',
  design: '설계',
  decision: '결정 대기',
  idea: '아이디어',
  observation: '관측',
}

export const PRIORITY_LABEL: Record<Priority, string> = {
  P0: 'P0 긴급',
  P1: 'P1 높음',
  P2: 'P2 보통',
  P3: 'P3 낮음',
}

export const PROJECT_STATUS_LABEL: Record<ProjectStatus, string> = {
  active: '활성',
  archived: '보관',
  deleted: '삭제',
}

export const MILESTONE_STATUS_LABEL: Record<MilestoneStatus, string> = {
  open: '열림',
  closed: '닫힘',
  deleted: '삭제됨',
}

export const KIND_LABEL: Record<ActivityKind, string> = {
  created: '생성',
  updated: '수정',
  commented: '댓글',
}

/** 활동 기록 `changes`의 필드 이름 */
export const FIELD_LABEL: Record<string, string> = {
  title: '제목',
  body: '본문',
  type: '종류',
  status: '상태',
  priority: '우선순위',
  labels: '라벨',
  parent: '상위 이슈',
  depends_on: '선행 이슈',
  refs: '참조',
  recheck_condition: '재검토 조건',
  topic: '주제',
  status_reason: '상태 사유',
  acknowledged: '확인',
  project: '프로젝트',
}

export function statusLabel(s: string): string {
  return (STATUS_LABEL as Record<string, string>)[s] ?? s
}

export function typeLabel(t: string): string {
  return (TYPE_LABEL as Record<string, string>)[t] ?? t
}

export function fieldLabel(f: string): string {
  return FIELD_LABEL[f] ?? f
}
