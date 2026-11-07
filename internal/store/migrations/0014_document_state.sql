-- Record documents get a state that is computed, not stored: the location
-- column goes, archived, imported and path come, and the relations of a
-- document (supersedes, related, about) move into document_relation. The
-- changes of the archive mark and the relations are kept in document_activity.
-- issue_ref gets the issue or document that an issue: or doc: reference
-- resolves to. A Go step runs after this file in the same transaction: it fills
-- document_relation from the front matter and resolves the existing references.
--
-- SQLite cannot drop a column that a CHECK or an index uses, so document is
-- rebuilt with the same ids; embedding.source_id points at them. similar_document
-- is derived and the embedding round rebuilds it, and it holds foreign keys to
-- document, so it is cleared first. document_fts keeps its rowids and is
-- indexed again below. Dropping the old table drops its triggers without firing
-- them; they are created again on the new table.

delete from similar_document;

create table document_new (
  id          integer primary key,
  project_id  integer not null references project(id),
  name        text not null,
  kind        text not null check (kind in ('decision','incident','report','implementation','other')),
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

-- A legacy document is imported, and archived when it lay under archive/. A
-- document of the old kind other is always legacy, so the new check holds.
insert into document_new(id, project_id, name, kind, date, title, tags, archived, imported, path, sha256, size, content,
    created_at, created_by, updated_at, updated_by)
  select id, project_id, name, kind, date, title, tags,
    case when location = 'archive' or (location = 'legacy' and name like 'archive/%') then 1 else 0 end,
    case when location = 'legacy' then 1 else 0 end,
    case when location = 'legacy' then name end,
    sha256, size, content, created_at, created_by, updated_at, updated_by
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

-- The target of a relation is a document, an issue or a text that resolved to
-- neither, exactly one of them.
create table document_relation (
  id                 integer primary key,
  document_id        integer not null references document(id),
  kind               text not null check (kind in ('supersedes','related','about')),
  target_document_id integer references document(id),
  target_issue_id    integer references issue(id),
  target_text        text,
  check ((target_document_id is not null) + (target_issue_id is not null) + (target_text is not null) = 1),
  check (kind = 'about' or target_issue_id is null),
  check (kind <> 'about' or target_document_id is null),
  check (target_document_id is null or target_document_id <> document_id)
);
create unique index document_relation_document on document_relation(document_id, kind, target_document_id) where target_document_id is not null;
create unique index document_relation_issue on document_relation(document_id, kind, target_issue_id) where target_issue_id is not null;
create unique index document_relation_text on document_relation(document_id, kind, target_text) where target_text is not null;
create index document_relation_target on document_relation(target_document_id, kind) where target_document_id is not null;
create index document_relation_target_issue on document_relation(target_issue_id) where target_issue_id is not null;

create table document_activity (
  id          integer primary key,
  document_id integer not null references document(id),
  actor_id    text not null references actor(id),
  session_id  text not null,
  client_host text not null,
  at          text not null,
  changes     text not null
);
create index document_activity_document on document_activity(document_id, id);

create table issue_ref_new (
  issue_id           integer not null references issue(id),
  ref                text not null,
  target_issue_id    integer references issue(id),
  target_document_id integer references document(id),
  primary key (issue_id, ref),
  check (target_issue_id is null or target_document_id is null)
);
insert into issue_ref_new(issue_id, ref) select issue_id, ref from issue_ref;
drop table issue_ref;
alter table issue_ref_new rename to issue_ref;
create index issue_ref_target_issue on issue_ref(target_issue_id) where target_issue_id is not null;
create index issue_ref_target_document on issue_ref(target_document_id) where target_document_id is not null;
