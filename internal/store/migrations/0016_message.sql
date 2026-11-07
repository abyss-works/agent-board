-- The message space: short-lived notes of a project. A message is never
-- removed; deleted_at marks a deletion and an expired message is only left
-- out of the default list.
create table message (
  id               integer primary key,
  project_id       integer not null references project(id),
  to_label         text check (to_label is null or length(to_label) between 1 and 64),
  title            text not null check (length(title) >= 1),
  body             text not null,
  created_at       text not null,
  created_by       text not null references actor(id),
  created_session  text not null,
  created_host     text not null,
  expires_at       text not null,
  deleted_at       text,
  deleted_by       text references actor(id)
);
create index message_project on message(project_id, deleted_at, expires_at);
