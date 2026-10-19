create table token (
  id           integer primary key,
  actor_id     text not null references actor(id),
  client_host  text not null,
  token_hash   text not null unique,
  created_at   text not null,
  created_by   text not null references actor(id),
  last_used_at text,
  revoked_at   text,
  revoked_by   text references actor(id)
);
create index token_actor on token(actor_id, client_host);
