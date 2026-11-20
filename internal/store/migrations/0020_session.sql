-- An agent session is an actor and a session ID the server has seen in the
-- trace headers of an authenticated request. The row is written on the first
-- request and last_seen_at, last_host follow later ones without touching
-- updated_at, which moves only when parent, role, issue or location change.
-- A session may name its parent session; both parent columns are set or
-- neither is. location is a JSON object. No row is made from past activity.
create table session (
  actor_id          text not null references actor(id),
  session_id        text not null,
  first_seen_at     text not null,
  last_seen_at      text not null,
  last_host         text not null,
  parent_actor_id   text,
  parent_session_id text,
  role              text check (role in ('main','subagent','runner')),
  issue_id          integer references issue(id),
  location          text,
  updated_at        text not null,
  primary key (actor_id, session_id),
  foreign key (parent_actor_id, parent_session_id) references session(actor_id, session_id),
  check ((parent_actor_id is null) = (parent_session_id is null))
);
create index session_parent on session(parent_actor_id, parent_session_id);
create index session_last_seen on session(last_seen_at);

-- The last project of a session is read from the records it left.
create index resource_activity_session on resource_activity(actor_id, session_id);
create index document_activity_session on document_activity(actor_id, session_id);
