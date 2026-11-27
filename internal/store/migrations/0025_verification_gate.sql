-- Opt-in user verification state and release gate columns on issue.
alter table issue add column verification_state text not null default 'none' check (verification_state in ('none','pending','verified'));
alter table issue add column verification_note text;
alter table issue add column verified_at text;
alter table issue add column verified_by text references actor(id);

create index if not exists idx_issue_verification_pending on issue(project_id, updated_at desc) where verification_state = 'pending';
