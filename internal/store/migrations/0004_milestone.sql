create table milestone (
  id           integer primary key,
  project_id   integer not null references project(id),
  name         text not null,
  description  text not null default '',
  target_date  text,
  status       text not null default 'open' check (status in ('open','closed','deleted')),
  closed_at    text,
  created_at   text not null,
  updated_at   text not null,
  created_by   text not null references actor(id),
  updated_by   text not null references actor(id)
);
create index milestone_project on milestone(project_id, status);

alter table issue add column milestone_id integer references milestone(id);
create index issue_milestone on issue(milestone_id);
