-- document accepts the location legacy (the folders of the earlier record
-- system and the subfolders of archive) and the kind other (a legacy document
-- whose kind is not known).
--
-- SQLite cannot change a CHECK constraint in place, so document is rebuilt
-- with the same ids. document_fts keeps its rows because it indexes by rowid;
-- dropping the old table drops its triggers without firing them, and they are
-- created again on the new table.

create table document_new (
  id          integer primary key,
  project_id  integer not null references project(id),
  name        text not null,
  location    text not null check (location in ('history','archive','legacy')),
  kind        text not null check (kind in ('decision','incident','report','implementation','other')),
  date        text,
  title       text,
  tags        text not null default '[]',
  sha256      text not null,
  size        integer not null,
  content     text not null,
  created_at  text not null,
  created_by  text not null references actor(id),
  updated_at  text not null,
  updated_by  text not null references actor(id),
  unique (project_id, name)
);

insert into document_new select id, project_id, name, location, kind, date, title, tags, sha256, size, content,
  created_at, created_by, updated_at, updated_by from document;
drop table document;
alter table document_new rename to document;

create trigger document_ai after insert on document begin
  insert into document_fts(rowid, name, title, tags, content) values (new.id, new.name, new.title, new.tags, new.content);
end;
create trigger document_ad after delete on document begin
  insert into document_fts(document_fts, rowid, name, title, tags, content) values ('delete', old.id, old.name, old.title, old.tags, old.content);
end;
create trigger document_au after update of name, title, tags, content on document begin
  insert into document_fts(document_fts, rowid, name, title, tags, content) values ('delete', old.id, old.name, old.title, old.tags, old.content);
  insert into document_fts(rowid, name, title, tags, content) values (new.id, new.name, new.title, new.tags, new.content);
end;
