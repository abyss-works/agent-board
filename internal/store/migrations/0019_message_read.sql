-- A reader marks a message as read. One row per message and reader; a row is
-- written once and never changed. The message row is not touched.
create table message_read (
  message_id    integer not null references message(id),
  reader        text not null check (length(reader) between 1 and 64),
  read_at       text not null,
  read_by       text not null references actor(id),
  read_session  text not null,
  read_host     text not null,
  primary key (message_id, reader)
);
