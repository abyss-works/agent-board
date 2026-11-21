import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useApi } from '@/app/state'
import { milestoneListParams } from '@/lib/milestones'
import { issueListQuery, type IssueListFilters, type SessionListParams } from './client'
import type {
  Activity,
  Actor,
  ActorToken,
  ActorTokenFilter,
  Agenda,
  Board,
  DeviceAuthorization,
  DeviceAuthorizationPatch,
  Issue,
  IssueCreate,
  IssuePatch,
  IssueSummary,
  LabelCount,
  Milestone,
  MilestoneCreate,
  MilestonePatch,
  MilestoneStatus,
  Overview,
  Page,
  RecordDocument,
  Project,
  ProjectCreate,
  ProjectPatch,
  QueueItem,
  Session,
  SessionListResponse,
  Template,
  Topic,
  TopicCreate,
  TopicPatch,
  TopicStatus,
} from './types'

/** 모든 질의 키는 이 값으로 시작한다. 쓰기 뒤 이 값으로 모든 조회를 다시 불러온다. */
export const QUERY_ROOT = 'agb'

export function useProjects() {
  const api = useApi()
  return useQuery({
    queryKey: [QUERY_ROOT, 'projects'],
    queryFn: ({ signal }) => api.get<Page<Project>>('/projects', { status: 'all', sort: 'key', limit: 200 }, signal),
  })
}

export function useOverview(projectKey: string | null) {
  const api = useApi()
  return useQuery({
    queryKey: [QUERY_ROOT, 'overview', projectKey],
    queryFn: ({ signal }) => api.get<Overview>(`/projects/${projectKey}/overview`, { limit: 50 }, signal),
    enabled: projectKey !== null,
  })
}

export function agendaQueryKey(projectKey: string | null) {
  return [QUERY_ROOT, 'agenda', projectKey] as const
}

/** 프로젝트 아젠다 조회(내 차례 대기 세션 포함). 프로젝트를 고르지 않으면 부르지 않는다. */
export function useAgenda(projectKey: string | null) {
  const api = useApi()
  return useQuery<Agenda>({
    queryKey: agendaQueryKey(projectKey),
    queryFn: ({ signal }) => api.getAgenda({ project: projectKey!, limit: 50 }, signal),
    enabled: projectKey !== null,
  })
}

export function useBoard(projectKey: string | null) {
  const api = useApi()
  return useQuery({
    queryKey: [QUERY_ROOT, 'board', projectKey],
    queryFn: ({ signal }) => api.get<Board>('/board', { project: projectKey, closed_days: 7, limit: 200 }, signal),
    placeholderData: keepPreviousData,
  })
}

export function useIssues(filters: IssueListFilters) {
  const api = useApi()
  const query = issueListQuery(filters)
  return useQuery({
    queryKey: [QUERY_ROOT, 'issues', query],
    queryFn: ({ signal }) => api.get<Page<IssueSummary>>(`/issues${query}`, undefined, signal),
    placeholderData: keepPreviousData,
  })
}

export function useIssue(key: string) {
  const api = useApi()
  return useQuery({
    queryKey: [QUERY_ROOT, 'issue', key],
    queryFn: ({ signal }) => api.get<Issue>(`/issues/${key}`, undefined, signal),
  })
}

export function useIssueActivity(key: string) {
  const api = useApi()
  return useQuery({
    queryKey: [QUERY_ROOT, 'issue-activity', key],
    queryFn: ({ signal }) => api.get<Page<Activity>>(`/issues/${key}/activity`, { limit: 200 }, signal),
  })
}

export interface ActivityFilters {
  project: string | null
  actor: string | null
  kind: string | null
  /** 세션 ID 필터. 서버 GET /v1/activity의 session 질의로 보낸다. */
  session?: string | null
  limit?: number
}

export function activityQueryKey(f: ActivityFilters) {
  return [QUERY_ROOT, 'activity', f.project, f.actor, f.kind, f.session ?? null, f.limit ?? 100] as const
}

export function useActivity(f: ActivityFilters, options?: { enabled?: boolean }) {
  const api = useApi()
  const session = f.session ?? null
  const limit = f.limit ?? 100
  return useQuery({
    queryKey: activityQueryKey(f),
    queryFn: ({ signal }) =>
      api.get<Page<Activity>>('/activity', { project: f.project, actor: f.actor, kind: f.kind, session, limit }, signal),
    placeholderData: keepPreviousData,
    enabled: options?.enabled ?? true,
  })
}

export function sessionsQueryKey(params?: SessionListParams) {
  return [
    QUERY_ROOT,
    'sessions',
    params?.project ?? null,
    params?.actor ?? null,
    params?.parent ?? null,
    params?.active ?? null,
    params?.named ?? null,
    params?.waiting ?? null,
    params?.limit ?? null,
    params?.offset ?? null,
  ] as const
}

/** 세션 목록 조회. 기본 갱신 정책을 상속한다. */
export function useSessions(params?: SessionListParams, options?: { enabled?: boolean }) {
  const api = useApi()
  return useQuery<SessionListResponse>({
    queryKey: sessionsQueryKey(params),
    queryFn: ({ signal }) => api.listSessions(params, signal),
    placeholderData: keepPreviousData,
    enabled: options?.enabled ?? true,
  })
}

export function sessionQueryKey(actor: string | null, sessionId: string | null) {
  return [QUERY_ROOT, 'session', actor, sessionId] as const
}

/** 세션 단건 조회(자식 세션 포함). 행위자와 세션 ID가 모두 있을 때만 호출한다. */
export function useSession(actor: string | null, sessionId: string | null) {
  const api = useApi()
  return useQuery<Session>({
    queryKey: sessionQueryKey(actor, sessionId),
    queryFn: ({ signal }) => api.getSession(actor!, sessionId!, signal),
    enabled: actor !== null && sessionId !== null,
  })
}

export interface DocumentFilters {
  project: string | null
  q: string
  kind: readonly string[]
  /** 비어 있으면 서버 기본인 유효 문서만 찾는다. */
  state: readonly string[]
}

/** GET /v1/documents. q가 있으면 관련도 순이고 스니펫이 붙는다. 없으면 날짜 최신 먼저다. 종류·상태는 쉼표 OR. */
export function useDocuments(f: DocumentFilters) {
  const api = useApi()
  const params = { project: f.project, q: f.q, kind: f.kind.join(','), state: f.state.join(','), limit: 200 }
  return useQuery({
    queryKey: [QUERY_ROOT, 'documents', params],
    queryFn: ({ signal }) => api.get<Page<RecordDocument>>('/documents', params, signal),
    placeholderData: keepPreviousData,
  })
}

export function useDocument(projectKey: string, name: string) {
  const api = useApi()
  return useQuery({
    queryKey: [QUERY_ROOT, 'document', projectKey, name],
    queryFn: ({ signal }) => api.get<RecordDocument>(`/projects/${projectKey}/documents/${encodeURIComponent(name)}`, undefined, signal),
  })
}

export function useQueue(projectKey: string | null, status: string, limit: number) {
  const api = useApi()
  return useQuery({
    queryKey: [QUERY_ROOT, 'queue', projectKey, status, limit],
    queryFn: ({ signal }) => api.get<Page<QueueItem>>('/queue', { project: projectKey, status, limit }, signal),
  })
}

export function useLabels(projectKey: string | null) {
  const api = useApi()
  return useQuery({
    queryKey: [QUERY_ROOT, 'labels', projectKey],
    queryFn: ({ signal }) => api.get<{ items: LabelCount[] }>('/labels', { project: projectKey }, signal),
  })
}

/** 프로젝트의 주제 목록. 대주제 이름순, 그 아래 주제 이름순이다. 프로젝트를 고르지 않으면 부르지 않는다. */
export function useTopics(projectKey: string | null, status: TopicStatus | 'all' = 'active') {
  const api = useApi()
  return useQuery({
    queryKey: [QUERY_ROOT, 'topics', projectKey, status],
    queryFn: ({ signal }) => api.get<{ items: Topic[] }>(`/projects/${projectKey}/topics`, { status: status === 'active' ? undefined : status }, signal),
    enabled: projectKey !== null,
  })
}

const OPEN_ONLY: readonly MilestoneStatus[] = ['open']

/**
 * 프로젝트의 마일스톤 목록. 기본은 열린 것만이고, 서버가 목표일 순(목표일이 없는 것은 뒤)으로 돌려준다.
 * 프로젝트를 고르지 않으면 부르지 않는다.
 */
export function useMilestones(projectKey: string | null, statuses: readonly MilestoneStatus[] = OPEN_ONLY) {
  const api = useApi()
  const params = milestoneListParams(statuses)
  return useQuery({
    queryKey: [QUERY_ROOT, 'milestones', projectKey, params.status ?? 'open'],
    queryFn: ({ signal }) => api.get<{ items: Milestone[] }>(`/projects/${projectKey}/milestones`, params, signal),
    enabled: projectKey !== null,
  })
}

/** 이슈 종류별 본문 템플릿. 자주 바뀌지 않으므로 주기적 갱신에서 뺀다. */
export function useTemplates() {
  const api = useApi()
  return useQuery({
    queryKey: [QUERY_ROOT, 'templates'],
    queryFn: ({ signal }) => api.get<{ items: Template[] }>('/templates', undefined, signal),
    refetchInterval: false,
    staleTime: 5 * 60_000,
  })
}

export function useActors() {
  const api = useApi()
  return useQuery({
    queryKey: [QUERY_ROOT, 'actors'],
    queryFn: ({ signal }) => api.get<{ items: Actor[] }>('/actors', undefined, signal),
    refetchInterval: false,
    staleTime: 60_000,
  })
}

function deviceAuthorizationKey(userCode: string | null) {
  return [QUERY_ROOT, 'device-authorization', userCode]
}

/**
 * 기기 승인 요청 한 건. 정규화한 코드(`XXXX-XXXX`)로 조회하고, 코드가 없으면 부르지 않는다.
 * 요청은 CLI가 토큰을 받아 가면 서버에서 지워지므로 주기적 갱신과 창 포커스 갱신에서 뺀다.
 * 승인·거부한 뒤 화면의 결과가 사라지지 않게 하려는 것이다.
 */
export function useDeviceAuthorization(userCode: string | null) {
  const api = useApi()
  return useQuery({
    queryKey: deviceAuthorizationKey(userCode),
    queryFn: ({ signal }) => api.get<DeviceAuthorization>(`/device-authorizations/${userCode}`, undefined, signal),
    enabled: userCode !== null,
    refetchInterval: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  })
}

/** 발급한 행위자 토큰 목록. 서버가 id 내림차순으로 돌려준다. */
export function useActorTokens(status: ActorTokenFilter) {
  const api = useApi()
  return useQuery({
    queryKey: [QUERY_ROOT, 'tokens', status],
    queryFn: ({ signal }) => api.get<{ items: ActorToken[] }>('/tokens', { status }, signal),
  })
}

function useInvalidateAll() {
  const qc = useQueryClient()
  return () => qc.invalidateQueries({ queryKey: [QUERY_ROOT] })
}

export function useCreateIssue() {
  const api = useApi()
  const invalidate = useInvalidateAll()
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
  return useMutation({
    mutationFn: ({ key, patch, version }: PatchIssueInput) => api.patch<Issue>(`/issues/${key}`, patch, { ifMatch: version }),
    onSuccess: (issue) => {
      qc.setQueryData([QUERY_ROOT, 'issue', issue.key], issue)
    },
    onSettled: () => qc.invalidateQueries({ queryKey: [QUERY_ROOT] }),
  })
}

export function useDeleteIssue() {
  const api = useApi()
  const invalidate = useInvalidateAll()
  return useMutation({
    mutationFn: ({ key, version, reason }: { key: string; version: number; reason: string }) =>
      api.delete<Issue>(`/issues/${key}`, { ifMatch: version, query: { reason } }),
    onSettled: () => invalidate(),
  })
}

/** 검증 필요(pending) 이슈를 검증 완료(verified)로 표시한다. */
export function useVerifyIssue() {
  const api = useApi()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ key, version }: { key: string; version?: number }) => api.verifyIssue(key, version),
    onSuccess: (issue) => {
      qc.setQueryData([QUERY_ROOT, 'issue', issue.key], issue)
    },
    onSettled: () => qc.invalidateQueries({ queryKey: [QUERY_ROOT] }),
  })
}

/** 검증 완료(verified) 이슈를 다시 검증 필요(pending)로 되돌린다. */
export function useUnverifyIssue() {
  const api = useApi()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ key, version }: { key: string; version?: number }) => api.unverifyIssue(key, version),
    onSuccess: (issue) => {
      qc.setQueryData([QUERY_ROOT, 'issue', issue.key], issue)
    },
    onSettled: () => qc.invalidateQueries({ queryKey: [QUERY_ROOT] }),
  })
}

export interface VerifyManyResult {
  done: number
  failed: number
  /** 처음 실패한 요청의 오류 */
  firstError: unknown
}

/** 키마다 검증 완료 요청을 차례로 보낸다. 일부가 실패해도 나머지는 계속한다. */
export function useVerifyMany() {
  const api = useApi()
  const invalidate = useInvalidateAll()
  return useMutation({
    mutationFn: async (keys: string[]): Promise<VerifyManyResult> => {
      const result: VerifyManyResult = { done: 0, failed: 0, firstError: undefined }
      for (const key of keys) {
        try {
          await api.verifyIssue(key)
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
  const invalidate = useInvalidateAll()
  return useMutation({
    mutationFn: ({ key, body }: { key: string; body: string }) => api.post<Activity>(`/issues/${key}/comments`, { body }),
    onSuccess: () => invalidate(),
  })
}

export function useCreateTopic() {
  const api = useApi()
  const invalidate = useInvalidateAll()
  return useMutation({
    mutationFn: ({ project, body }: { project: string; body: TopicCreate }) => api.post<Topic>(`/projects/${project}/topics`, body),
    onSuccess: () => invalidate(),
  })
}

export function usePatchTopic() {
  const api = useApi()
  const invalidate = useInvalidateAll()
  return useMutation({
    mutationFn: ({ id, patch }: { id: number; patch: TopicPatch }) => api.patch<Topic>(`/topics/${id}`, patch),
    onSuccess: () => invalidate(),
  })
}

export function useDeleteTopic() {
  const api = useApi()
  const invalidate = useInvalidateAll()
  return useMutation({
    mutationFn: (id: number) => api.delete<Topic>(`/topics/${id}`),
    onSuccess: () => invalidate(),
  })
}

export function useCreateMilestone() {
  const api = useApi()
  const invalidate = useInvalidateAll()
  return useMutation({
    mutationFn: ({ project, body }: { project: string; body: MilestoneCreate }) => api.post<Milestone>(`/projects/${project}/milestones`, body),
    onSuccess: () => invalidate(),
  })
}

export function usePatchMilestone() {
  const api = useApi()
  const invalidate = useInvalidateAll()
  return useMutation({
    mutationFn: ({ id, patch }: { id: number; patch: MilestonePatch }) => api.patch<Milestone>(`/milestones/${id}`, patch),
    onSuccess: () => invalidate(),
  })
}

export function useDeleteMilestone() {
  const api = useApi()
  const invalidate = useInvalidateAll()
  return useMutation({
    mutationFn: (id: number) => api.delete<Milestone>(`/milestones/${id}`),
    onSuccess: () => invalidate(),
  })
}

export function useCreateProject() {
  const api = useApi()
  const invalidate = useInvalidateAll()
  return useMutation({
    mutationFn: (body: ProjectCreate) => api.post<Project>('/projects', body),
    onSuccess: () => invalidate(),
  })
}

export function usePatchProject() {
  const api = useApi()
  const invalidate = useInvalidateAll()
  return useMutation({
    mutationFn: ({ key, patch }: { key: string; patch: ProjectPatch }) => api.patch<Project>(`/projects/${key}`, patch),
    onSuccess: () => invalidate(),
  })
}

export function useDeleteProject() {
  const api = useApi()
  const invalidate = useInvalidateAll()
  return useMutation({
    mutationFn: (key: string) => api.delete<Project>(`/projects/${key}`),
    onSuccess: () => invalidate(),
  })
}

/** 대기 중인 기기 승인 요청을 승인하거나 거부한다. 응답을 조회 결과로 바로 쓴다. 다시 불러오면 CLI가 토큰을 받아 간 뒤에는 요청이 없어 결과가 사라진다. */
export function useDecideDeviceAuthorization() {
  const api = useApi()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ userCode, status }: { userCode: string; status: DeviceAuthorizationPatch['status'] }) =>
      api.patch<DeviceAuthorization>(`/device-authorizations/${userCode}`, { status }),
    onSuccess: (auth, { userCode }) => {
      qc.setQueryData(deviceAuthorizationKey(userCode), auth)
    },
  })
}

/** 활성 토큰 하나를 폐기한다. 폐기한 뒤 이 인스턴스의 조회를 다시 불러온다. */
export function useRevokeActorToken() {
  const api = useApi()
  const invalidate = useInvalidateAll()
  return useMutation({
    mutationFn: (id: number) => api.delete<ActorToken>(`/tokens/${id}`),
    onSuccess: () => invalidate(),
  })
}
