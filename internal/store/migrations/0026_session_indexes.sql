-- Add composite (actor_id, session_id) indexes on activity and issue_claim
-- so session last_project lookups avoid full scans.
create index if not exists activity_actor_session on activity(actor_id, session_id);
create index if not exists idx_issue_claim_session on issue_claim(actor_id, session_id);
