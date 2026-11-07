-- The kind of a record document is a free string. The database only checks its
-- length and that the kind other belongs to an imported document; the format
-- is checked by the model.
--
-- SQLite cannot change a CHECK in place, so document is rebuilt with the same
-- ids, as in 0014. Foreign keys are enforced inside the migration transaction,
-- and the tables that point at document would block dropping it: their rows
-- are set aside, emptied and put back after the rebuild. similar_document is
-- derived and the embedding round rebuilds it, so it is only cleared.
-- document_fts keeps its rowids and is indexed again below. Dropping the old
-- table drops its triggers without firing them; they are created again on the
-- new table.

delete from similar_document;

create temp table keep_document_relation as select * from document_relation;
create temp table keep_document_activity as select * from document_activity;
create temp table keep_issue_ref as select * from issue_ref;
delete from document_relation;
delete from document_activity;
delete from issue_ref;

create table document_new (
  id          integer primary key,
  project_id  integer not null references project(id),
  name        text not null,
  kind        text not null check (length(kind) between 1 and 32),
  date        text,
  title       text,
  tags        text not null default '[]',
  archived    integer not null default 0 check (archived in (0,1)),
  imported    integer not null default 0 check (imported in (0,1)),
  path        text,
  sha256      text not null,
  size        integer not null,
  content     text not null,
  created_at  text not null,
  created_by  text not null references actor(id),
  updated_at  text not null,
  updated_by  text not null references actor(id),
  unique (project_id, name),
  check (kind <> 'other' or imported = 1)
);

insert into document_new(id, project_id, name, kind, date, title, tags, archived, imported, path, sha256, size, content,
    created_at, created_by, updated_at, updated_by)
  select id, project_id, name, kind, date, title, tags, archived, imported, path, sha256, size, content,
    created_at, created_by, updated_at, updated_by
  from document;
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
insert into document_fts(document_fts) values ('rebuild');

insert into document_relation select * from keep_document_relation;
insert into document_activity select * from keep_document_activity;
insert into issue_ref select * from keep_issue_ref;
drop table keep_document_relation;
drop table keep_document_activity;
drop table keep_issue_ref;
