-- The todo status and the decision type leave issue, and issue gains the
-- decision_needed column. Issues in todo move to backlog. Issues of type
-- decision become design issues that ask their title as the question.
--
-- SQLite cannot change a CHECK constraint in place, so issue is rebuilt. The
-- foreign keys are checked at commit: dropping the old table counts every row
-- that points at it, and inserting the rows again with the same ids settles
-- the count.

pragma defer_foreign_keys = on;

create table issue_old as select * from issue;
drop table issue;

create table issue (
  id                integer primary key,
  project_id        integer not null references project(id),
  number            integer not null,
  key               text not null unique,
  title             text not null,
  body              text not null default '',
  type              text not null check (type in ('feature','bug','design','idea','observation')),
  status            text not null check (status in ('backlog','doing','review','done','cancelled','deleted')),
  priority          text not null check (priority in ('P0','P1','P2','P3')),
  parent_id         integer references issue(id),
  recheck_condition text,
  decision_needed   text,
  topic_id          integer references topic(id),
  milestone_id      integer references milestone(id),
  status_reason     text,
  acknowledged_at   text,
  acknowledged_by   text references actor(id),
  version           integer not null default 1,
  created_at        text not null,
  updated_at        text not null,
  closed_at         text,
  status_changed_at text not null,
  status_changed_by text not null references actor(id),
  created_by        text not null references actor(id),
  updated_by        text not null references actor(id),
  unique (project_id, number)
);

insert into issue (id, project_id, number, key, title, body, type, status, priority, parent_id, recheck_condition, decision_needed,
                   topic_id, milestone_id, status_reason, acknowledged_at, acknowledged_by, version, created_at, updated_at, closed_at,
                   status_changed_at, status_changed_by, created_by, updated_by)
select id, project_id, number, key, title, body,
       case type when 'decision' then 'design' else type end,
       case status when 'todo' then 'backlog' else status end,
       priority, parent_id, recheck_condition,
       case type when 'decision' then substr(title, 1, 500) end,
       topic_id, milestone_id, status_reason, acknowledged_at, acknowledged_by, version, created_at, updated_at, closed_at,
       status_changed_at, status_changed_by, created_by, updated_by
from issue_old
order by id;

drop table issue_old;

create index issue_project_status on issue(project_id, status, priority, created_at);
create index issue_parent on issue(parent_id);
create index issue_milestone on issue(milestone_id);

create trigger issue_ai after insert on issue begin
  insert into issue_fts(rowid, key, title, body) values (new.id, new.key, new.title, new.body);
end;
create trigger issue_ad after delete on issue begin
  insert into issue_fts(issue_fts, rowid, key, title, body) values ('delete', old.id, old.key, old.title, old.body);
end;
create trigger issue_au after update of key, title, body on issue begin
  insert into issue_fts(issue_fts, rowid, key, title, body) values ('delete', old.id, old.key, old.title, old.body);
  insert into issue_fts(rowid, key, title, body) values (new.id, new.key, new.title, new.body);
end;
