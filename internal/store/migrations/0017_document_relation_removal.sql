-- A removed document relation stays as a row. removed_at marks the removal and
-- removed_by, removed_session and removed_host say who removed it; a relation
-- that is added again clears the marks. The unique and lookup indexes cover
-- live relations only, so a removed relation does not hold its target.
--
-- Adding a column leaves the rows and the foreign keys of document_relation as
-- they are. The indexes are dropped and created again with the new condition.
alter table document_relation add column removed_at text;
alter table document_relation add column removed_by text references actor(id);
alter table document_relation add column removed_session text;
alter table document_relation add column removed_host text;

drop index document_relation_document;
drop index document_relation_issue;
drop index document_relation_text;
drop index document_relation_target;
drop index document_relation_target_issue;
create unique index document_relation_document on document_relation(document_id, kind, target_document_id) where target_document_id is not null and removed_at is null;
create unique index document_relation_issue on document_relation(document_id, kind, target_issue_id) where target_issue_id is not null and removed_at is null;
create unique index document_relation_text on document_relation(document_id, kind, target_text) where target_text is not null and removed_at is null;
create index document_relation_target on document_relation(target_document_id, kind) where target_document_id is not null and removed_at is null;
create index document_relation_target_issue on document_relation(target_issue_id) where target_issue_id is not null and removed_at is null;
