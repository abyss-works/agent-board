create table actor (
  id         text primary key,
  kind       text not null check (kind in ('user', 'agent')),
  created_at text not null
);

create table project (
  id          integer primary key,
  key         text not null unique,
  name        text not null,
  status      text not null check (status in ('active','archived','deleted')),
  next_number integer not null default 1,
  created_at  text not null,
  updated_at  text not null,
  created_by  text not null references actor(id),
  updated_by  text not null references actor(id)
);

create table issue (
  id                integer primary key,
  project_id        integer not null references project(id),
  number            integer not null,
  key               text not null unique,
  title             text not null,
  body              text not null default '',
  type              text not null check (type in ('feature','bug','design','decision','idea','observation')),
  status            text not null check (status in ('backlog','todo','doing','review','done','cancelled','deleted')),
  priority          text not null check (priority in ('P0','P1','P2','P3')),
  parent_id         integer references issue(id),
  recheck_condition text,
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
create index issue_project_status on issue(project_id, status, priority, created_at);
create index issue_parent on issue(parent_id);

create table issue_label (
  issue_id integer not null references issue(id),
  label    text not null,
  primary key (issue_id, label)
);
create index issue_label_label on issue_label(label);

create table issue_dependency (
  issue_id      integer not null references issue(id),
  depends_on_id integer not null references issue(id),
  primary key (issue_id, depends_on_id)
);
create index issue_dependency_rev on issue_dependency(depends_on_id);

create table issue_ref (
  issue_id integer not null references issue(id),
  ref      text not null,
  primary key (issue_id, ref)
);

create table activity (
  id          integer primary key,
  issue_id    integer not null references issue(id),
  kind        text not null check (kind in ('created','updated','commented')),
  actor_id    text not null references actor(id),
  session_id  text not null,
  client_host text not null,
  at          text not null,
  changes     text,
  body        text
);
create index activity_issue on activity(issue_id, id);
create index activity_at on activity(at);
create index activity_session on activity(session_id);

create virtual table issue_fts using fts5(
  key, title, body,
  content='issue', content_rowid='id',
  tokenize='unicode61'
);

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
