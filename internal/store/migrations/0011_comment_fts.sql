-- A search index over comments: the summary and the body of the activity
-- records. Only comments carry either, so only comments yield terms; every
-- record is still indexed because an external content index must hold a row
-- for each row of its table to pass the integrity check. Activity records are
-- never changed or deleted, so an insert trigger keeps the index.

create virtual table comment_fts using fts5(
  summary, body,
  content='activity', content_rowid='id',
  tokenize='unicode61'
);

insert into comment_fts(rowid, summary, body) select id, summary, body from activity;

create trigger activity_ai after insert on activity begin
  insert into comment_fts(rowid, summary, body) values (new.id, new.summary, new.body);
end;
