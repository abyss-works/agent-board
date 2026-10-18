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
  Template,
  Topic,
  TopicCreate,
  TopicPatch,
  TopicStatus,
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

/** 프로젝트의 주제 목록. 대주제 이름순, 그 아래 주제 이름순이다. 프로젝트를 고르지 않으면 부르지 않는다. */
export function useTopics(projectKey: string | null, status: TopicStatus | 'all' = 'active') {
  const api = useApi()
  const inst = useKeyPrefix()
  return useQuery({
    queryKey: [inst, 'topics', projectKey, status],
    queryFn: ({ signal }) => api.get<{ items: Topic[] }>(`/projects/${projectKey}/topics`, { status: status === 'active' ? undefined : status }, signal),
    enabled: projectKey !== null,
  })
}

/** 이슈 종류별 본문 템플릿. 자주 바뀌지 않으므로 3초 갱신에서 뺀다. */
export function useTemplates() {
  const api = useApi()
  const inst = useKeyPrefix()
  return useQuery({
    queryKey: [inst, 'templates'],
    queryFn: ({ signal }) => api.get<{ items: Template[] }>('/templates', undefined, signal),
    refetchInterval: false,
    staleTime: 5 * 60_000,
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
    mutationFn: ({ key, version, reason }: { key: string; version: number; reason: string }) =>
      api.delete<Issue>(`/issues/${key}`, { ifMatch: version, query: { reason } }),
    onSettled: () => invalidate(),
  })
}

/** 이슈 하나의 확인 표시를 켜거나 끈다. If-Match는 보내지 않아 다른 변경과 겹쳐도 실패하지 않는다. */
export function useSetAcknowledged() {
  const api = useApi()
  const qc = useQueryClient()
  const inst = useKeyPrefix()
  return useMutation({
    mutationFn: ({ key, acknowledged }: { key: string; acknowledged: boolean }) =>
      acknowledged ? api.post<Issue>(`/issues/${key}/acknowledge`, undefined) : api.delete<Issue>(`/issues/${key}/acknowledge`),
    onSuccess: (issue) => {
      qc.setQueryData([inst, 'issue', issue.key], issue)
    },
    onSettled: () => qc.invalidateQueries({ queryKey: [inst] }),
  })
}

export interface AcknowledgeManyResult {
  done: number
  failed: number
  /** 처음 실패한 요청의 오류 */
  firstError: unknown
}

/** 키마다 확인 요청을 차례로 보낸다. 일부가 실패해도 나머지는 계속한다. */
export function useAcknowledgeMany() {
  const api = useApi()
  const invalidate = useInvalidateInstance()
  return useMutation({
    mutationFn: async (keys: string[]): Promise<AcknowledgeManyResult> => {
      const result: AcknowledgeManyResult = { done: 0, failed: 0, firstError: undefined }
      for (const key of keys) {
        try {
          await api.post<Issue>(`/issues/${key}/acknowledge`, undefined)
          result.done += 1
        } catch (err) {
          result.failed += 1
          result.firstError ??= err
        }
      }
      return result
    },
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

export function useCreateTopic() {
  const api = useApi()
  const invalidate = useInvalidateInstance()
  return useMutation({
    mutationFn: ({ project, body }: { project: string; body: TopicCreate }) => api.post<Topic>(`/projects/${project}/topics`, body),
    onSuccess: () => invalidate(),
  })
}

export function usePatchTopic() {
  const api = useApi()
  const invalidate = useInvalidateInstance()
  return useMutation({
    mutationFn: ({ id, patch }: { id: number; patch: TopicPatch }) => api.patch<Topic>(`/topics/${id}`, patch),
    onSuccess: () => invalidate(),
  })
}

export function useDeleteTopic() {
  const api = useApi()
  const invalidate = useInvalidateInstance()
  return useMutation({
    mutationFn: (id: number) => api.delete<Topic>(`/topics/${id}`),
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
