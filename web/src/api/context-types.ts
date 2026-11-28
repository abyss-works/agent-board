export type GraphNodeKind = 'issue' | 'spec' | 'record' | 'epic'
export type EdgeKind = 'supersedes' | 'about' | 'depends_on' | 'related'

export interface ContextNeighbor {
  edge_id: number
  direction: 'outbound' | 'inbound'
  kind: EdgeKind
  origin: string
  status: string
  node_type: GraphNodeKind
  node_id: number
  key_or_name: string
  title: string
  node_status?: string
  summary: string
  content: string | null
  hop?: number
}

export interface ContextItem {
  node_type: GraphNodeKind
  node_id: number
  project: string
  key_or_name: string
  kind: string
  title: string
  score: number
  summary: string
  content: string
  superseded_by: ContextNeighbor | null
  neighbors: ContextNeighbor[]
}

export interface ContextResponse {
  query: string
  cutoff_applied: boolean
  cutoff_threshold: number
  score_gap_1_2: number
  reranked: boolean
  fallback_used: boolean
  fallback_mode: string
  cache_hit: boolean
  timing_ms: { embed: number; db: number; rerank: number; total: number }
  items: ContextItem[]
}

export interface ContextRequest {
  query: string
  project?: string
  source_types?: string[]
  max_results?: number
  expand_hops?: number
  include_archived?: boolean
  include_superseded?: boolean
}

export interface EdgeCandidate {
  id: number
  project?: string
  source_type: GraphNodeKind
  source_id: number
  source_key_or_name?: string
  target_type?: GraphNodeKind
  target_id?: number
  target_key_or_name?: string
  unresolved_target_text?: string
  kind: EdgeKind
  status: string
  confidence?: number
  evidence?: string
}

export type StageStatus = 'planned' | 'active' | 'closed'

export interface StageMilestone {
  id: number
  project: string
  name: string
  description: string
  target_date: string | null
  status: StageStatus | 'deleted'
  progress: { total: number; done: number }
}

export interface Epic {
  key: string
  project: string
  title: string
  description: string
  status: 'open' | 'closed' | 'deleted'
}

export interface SpecRevision {
  id: number
  revision_no: number
  title: string
  content: string
  sha256: string
  size_bytes: number
  change_summary: string
  created_at: string
  created_by: string
}

export interface SpecSummary {
  project: string
  slug: string
  kind: string
  title: string
  current_revision_no: number
  archived: boolean
  updated_at: string
  updated_by: string
}
