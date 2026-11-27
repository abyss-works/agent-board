-- Append-only unified event stream for polling and SSE subscriptions.
-- IDs are monotonically increasing and never reused.
create table event (
  id         integer primary key autoincrement,
  at         text not null,
  project_id integer references project(id),
  kind       text not null,
  subject    text not null,
  actor      text not null,
  session_id text not null,
  data       text not null default '{}'
);
create index idx_event_project_id on event(project_id, id);
create index idx_event_kind_id on event(kind, id);
