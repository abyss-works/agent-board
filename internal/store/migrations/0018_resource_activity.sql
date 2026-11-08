-- The writes to projects, topics and milestones are recorded. A record names
-- the resource, who wrote it (actor, session, device), when, and the fields it
-- changed with their previous and new values, in the form of the changes of an
-- issue activity. activity belongs to one issue and document_activity to one
-- document, so these three resources, which share one shape, get one table.
-- resource_id is the id in the table of the resource; project_id is the project
-- itself for a project record and the project of the resource otherwise.
create table resource_activity (
  id          integer primary key,
  project_id  integer not null references project(id),
  resource    text not null check (resource in ('project','topic','milestone')),
  resource_id integer not null,
  kind        text not null check (kind in ('created','updated')),
  actor_id    text not null references actor(id),
  session_id  text not null,
  client_host text not null,
  at          text not null,
  changes     text not null
);
create index resource_activity_project on resource_activity(project_id, id);
create index resource_activity_resource on resource_activity(resource, resource_id, id);

-- A revoked token also keeps the session and device of the revoking request.
alter table token add column revoked_session text;
alter table token add column revoked_host text;
