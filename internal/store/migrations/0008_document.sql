-- Record documents: the original markdown of the record files of a project's
-- management repository. A document is never edited or deleted; only its
-- location (history or archive) changes when the file moves. tags holds the
-- front matter tags as a JSON array.

create table document (
  id          integer primary key,
  project_id  integer not null references project(id),
  name        text not null,
  location    text not null check (location in ('history','archive')),
  kind        text not null check (kind in ('decision','incident','report','implementation')),
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

create virtual table document_fts using fts5(
  name, title, tags, content,
  content='document', content_rowid='id',
  tokenize='unicode61'
);

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
