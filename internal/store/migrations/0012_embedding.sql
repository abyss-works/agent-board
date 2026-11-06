-- Embeddings of the searchable sources, one row per chunk of text. The source
-- is an issue, a comment (activity row) or a record document, source_id its
-- row id. text_sha256 is the hash of the chunk text and tells whether the
-- stored vector is current; vector is a little-endian float32 array.

create table embedding (
  source      text not null check (source in ('issue','comment','document')),
  source_id   integer not null,
  chunk       integer not null,
  model       text not null,
  text_sha256 text not null,
  text        text not null,
  vector      blob not null,
  created_at  text not null,
  primary key (source, source_id, chunk)
);
