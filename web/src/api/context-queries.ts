import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useApi } from '@/app/state'
import { QUERY_ROOT } from './queries'
import type { ContextRequest, ContextResponse, EdgeCandidate, Epic, SpecRevision, SpecSummary, StageMilestone, StageStatus } from './context-types'
import type { Page } from './types'

/** 통합 조회는 POST지만 읽기 전용이다. 같은 질문은 질의 캐시로 다시 받지 않는다. */
export function useContextSearch(req: ContextRequest | null) {
  const api = useApi()
  return useQuery({
    queryKey: [QUERY_ROOT, 'context', req],
    queryFn: () => api.post<ContextResponse>('/context/query', req),
    enabled: req !== null && req.query.trim() !== '',
    staleTime: 30_000,
    refetchInterval: false,
  })
}

export function useEdgeCandidates(project: string | null, kind: string | null) {
  const api = useApi()
  return useQuery({
    queryKey: [QUERY_ROOT, 'edge-candidates', project, kind],
    queryFn: ({ signal }) =>
      api.get<Page<EdgeCandidate>>('/edges/candidates', { project: project ?? undefined, kind: kind ?? undefined, limit: 100 }, signal),
  })
}

export function useDecideEdge() {
  const api = useApi()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, action }: { id: number; action: 'confirm' | 'reject' }) => api.post<EdgeCandidate>(`/edges/${id}/${action}`, undefined),
    onSuccess: () => qc.invalidateQueries({ queryKey: [QUERY_ROOT] }),
  })
}

export function useEpics(project: string | null) {
  const api = useApi()
  return useQuery({
    queryKey: [QUERY_ROOT, 'epics', project],
    queryFn: ({ signal }) => api.get<Page<Epic>>(`/projects/${project}/epics`, { status: 'open,closed', limit: 200 }, signal),
    enabled: project !== null,
  })
}

export function useStageMilestones(project: string | null) {
  const api = useApi()
  return useQuery({
    queryKey: [QUERY_ROOT, 'stage-milestones', project],
    queryFn: ({ signal }) => api.get<{ items: StageMilestone[] }>(`/projects/${project}/milestones`, { status: 'all' }, signal),
    enabled: project !== null,
  })
}

export function useSetMilestoneStage() {
  const api = useApi()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, status }: { id: number; status: StageStatus }) => api.patch<StageMilestone>(`/milestones/${id}`, { status }),
    onSuccess: () => qc.invalidateQueries({ queryKey: [QUERY_ROOT] }),
  })
}

export function useSpecs(project: string | null) {
  const api = useApi()
  return useQuery({
    queryKey: [QUERY_ROOT, 'specs', project],
    queryFn: ({ signal }) => api.get<Page<SpecSummary>>(`/projects/${project}/specs`, { limit: 200 }, signal),
    enabled: project !== null,
  })
}

export function useSpecRevisions(project: string, slug: string) {
  const api = useApi()
  return useQuery({
    queryKey: [QUERY_ROOT, 'spec-revisions', project, slug],
    queryFn: ({ signal }) =>
      api.get<{ items: SpecRevision[] }>(`/projects/${project}/specs/${encodeURIComponent(slug)}/revisions`, undefined, signal),
  })
}
