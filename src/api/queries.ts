import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useApi, useAppState } from '@/app/state'
import { issueListQuery, type IssueListFilters } from './client'
import type {
  Activity,
  Actor,
  Board,
  Issue,
  IssueCreate,
  IssuePatch,
  IssueSummary,
  LabelCount,
  Overview,
  Page,
  Project,
  ProjectCreate,
  ProjectPatch,
  QueueItem,
} from './types'

/** 모든 질의 키는 인스턴스 ID로 시작한다. 인스턴스를 바꾸면 캐시가 섞이지 않는다. */
function useKeyPrefix() {
  return useAppState().instance.id
}

export function useProjects() {
  const api = useApi()
  const inst = useKeyPrefix()
  return useQuery({
    queryKey: [inst, 'projects'],
    queryFn: ({ signal }) => api.get<Page<Project>>('/projects', { status: 'all', sort: 'key', limit: 200 }, signal),
  })
}

export function useOverview(projectKey: string | null) {
  const api = useApi()
  const inst = useKeyPrefix()
  return useQuery({
    queryKey: [inst, 'overview', projectKey],
    queryFn: ({ signal }) => api.get<Overview>(`/projects/${projectKey}/overview`, { limit: 10 }, signal),
    enabled: projectKey !== null,
  })
}

export function useBoard(projectKey: string | null) {
  const api = useApi()
  const inst = useKeyPrefix()
  return useQuery({
    queryKey: [inst, 'board', projectKey],
    queryFn: ({ signal }) => api.get<Board>('/board', { project: projectKey, closed_days: 7, limit: 200 }, signal),
    placeholderData: keepPreviousData,
  })
}

export function useIssues(filters: IssueListFilters) {
  const api = useApi()
  const inst = useKeyPrefix()
  const query = issueListQuery(filters)
  return useQuery({
    queryKey: [inst, 'issues', query],
    queryFn: ({ signal }) => api.get<Page<IssueSummary>>(`/issues${query}`, undefined, signal),
    placeholderData: keepPreviousData,
  })
}

export function useIssue(key: string) {
  const api = useApi()
  const inst = useKeyPrefix()
  return useQuery({
    queryKey: [inst, 'issue', key],
    queryFn: ({ signal }) => api.get<Issue>(`/issues/${key}`, undefined, signal),
  })
}

export function useIssueActivity(key: string) {
  const api = useApi()
  const inst = useKeyPrefix()
  return useQuery({
    queryKey: [inst, 'issue-activity', key],
    queryFn: ({ signal }) => api.get<Page<Activity>>(`/issues/${key}/activity`, { limit: 200 }, signal),
  })
}

export interface ActivityFilters {
  project: string | null
  actor: string | null
  kind: string | null
}

export function useActivity(f: ActivityFilters) {
  const api = useApi()
  const inst = useKeyPrefix()
  return useQuery({
    queryKey: [inst, 'activity', f.project, f.actor, f.kind],
    queryFn: ({ signal }) =>
      api.get<Page<Activity>>('/activity', { project: f.project, actor: f.actor, kind: f.kind, limit: 100 }, signal),
    placeholderData: keepPreviousData,
  })
}

export function useQueue(projectKey: string | null, status: string, limit: number) {
  const api = useApi()
  const inst = useKeyPrefix()
  return useQuery({
    queryKey: [inst, 'queue', projectKey, status, limit],
    queryFn: ({ signal }) => api.get<Page<QueueItem>>('/queue', { project: projectKey, status, limit }, signal),
  })
}

export function useLabels(projectKey: string | null) {
  const api = useApi()
  const inst = useKeyPrefix()
  return useQuery({
    queryKey: [inst, 'labels', projectKey],
    queryFn: ({ signal }) => api.get<{ items: LabelCount[] }>('/labels', { project: projectKey }, signal),
  })
}

export function useActors() {
  const api = useApi()
  const inst = useKeyPrefix()
  return useQuery({
    queryKey: [inst, 'actors'],
    queryFn: ({ signal }) => api.get<{ items: Actor[] }>('/actors', undefined, signal),
    refetchInterval: false,
    staleTime: 60_000,
  })
}

/** 쓰기 뒤 이 인스턴스의 모든 조회를 다시 불러온다. */
function useInvalidateInstance() {
  const qc = useQueryClient()
  const inst = useKeyPrefix()
  return () => qc.invalidateQueries({ queryKey: [inst] })
}

export function useCreateIssue() {
  const api = useApi()
  const invalidate = useInvalidateInstance()
  return useMutation({
    mutationFn: (body: IssueCreate) => api.post<Issue>('/issues', body),
    onSuccess: () => invalidate(),
  })
}

export interface PatchIssueInput {
  key: string
  patch: IssuePatch
  /** 현재 version. If-Match로 보내 다른 곳의 변경을 덮어쓰지 않는다. */
  version: number
}

export function usePatchIssue() {
  const api = useApi()
  const qc = useQueryClient()
  const inst = useKeyPrefix()
  return useMutation({
    mutationFn: ({ key, patch, version }: PatchIssueInput) => api.patch<Issue>(`/issues/${key}`, patch, { ifMatch: version }),
    onSuccess: (issue) => {
      qc.setQueryData([inst, 'issue', issue.key], issue)
    },
    onSettled: () => qc.invalidateQueries({ queryKey: [inst] }),
  })
}

export function useDeleteIssue() {
  const api = useApi()
  const invalidate = useInvalidateInstance()
  return useMutation({
    mutationFn: ({ key, version }: { key: string; version: number }) => api.delete<Issue>(`/issues/${key}`, { ifMatch: version }),
    onSettled: () => invalidate(),
  })
}

export function useCreateComment() {
  const api = useApi()
  const invalidate = useInvalidateInstance()
  return useMutation({
    mutationFn: ({ key, body }: { key: string; body: string }) => api.post<Activity>(`/issues/${key}/comments`, { body }),
    onSuccess: () => invalidate(),
  })
}

export function useCreateProject() {
  const api = useApi()
  const invalidate = useInvalidateInstance()
  return useMutation({
    mutationFn: (body: ProjectCreate) => api.post<Project>('/projects', body),
    onSuccess: () => invalidate(),
  })
}

export function usePatchProject() {
  const api = useApi()
  const invalidate = useInvalidateInstance()
  return useMutation({
    mutationFn: ({ key, patch }: { key: string; patch: ProjectPatch }) => api.patch<Project>(`/projects/${key}`, patch),
    onSuccess: () => invalidate(),
  })
}

export function useDeleteProject() {
  const api = useApi()
  const invalidate = useInvalidateInstance()
  return useMutation({
    mutationFn: (key: string) => api.delete<Project>(`/projects/${key}`),
    onSuccess: () => invalidate(),
  })
}
