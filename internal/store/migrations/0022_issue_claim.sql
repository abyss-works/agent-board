-- Stored issue claims with session-based expiration. Only one open claim
-- (released_at IS NULL) may exist per issue at a time.
create table issue_claim (
  id          integer primary key,
  issue_id    integer not null references issue(id),
  actor_id    text not null,
  session_id  text not null,
  claimed_at  text not null,
  released_at text,
  outcome     text check (outcome is null or outcome in ('done', 'abandoned', 'expired', 'taken_over')),
  reason      text,
  check ((released_at is null and outcome is null) or (released_at is not null and outcome is not null))
);
create unique index idx_issue_claim_open on issue_claim(issue_id) where released_at is null;
create index idx_issue_claim_issue on issue_claim(issue_id, id desc);
create index idx_issue_claim_session_open on issue_claim(actor_id, session_id) where released_at is null;
