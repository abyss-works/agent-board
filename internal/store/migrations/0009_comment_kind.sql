-- Comment kind: decision, finding or lesson on a comment that carries
-- knowledge. Null on progress notes and on records that are not comments.
alter table activity add column comment_kind text;
