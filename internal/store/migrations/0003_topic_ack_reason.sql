create table topic (
  id          integer primary key,
  project_id  integer not null references project(id),
  name        text not null,
  parent_id   integer references topic(id),
  status      text not null default 'active' check (status in ('active','deleted')),
  created_at  text not null,
  updated_at  text not null,
  created_by  text not null references actor(id),
  updated_by  text not null references actor(id)
);
create index topic_project on topic(project_id, parent_id);

alter table issue add column topic_id integer references topic(id);
alter table issue add column status_reason text;
alter table issue add column acknowledged_at text;
alter table issue add column acknowledged_by text references actor(id);

-- Issues that exist before this migration count as acknowledged at migration time.
update issue set acknowledged_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now');
