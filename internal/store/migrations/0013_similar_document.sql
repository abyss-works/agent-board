-- Record documents that are close in meaning, found from their embeddings:
-- for each document the nearest other documents above a score floor. The
-- table is derived and rebuilt whole after an embedding round changes rows.

create table similar_document (
  document_id integer not null references document(id),
  similar_id  integer not null references document(id),
  score       real not null,
  model       text not null,
  primary key (document_id, similar_id)
);
