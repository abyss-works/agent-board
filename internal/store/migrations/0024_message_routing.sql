-- Routing targets and thread replies on messages: target session, target role,
-- related issue, and parent message for replies.
alter table message add column to_actor text references actor(id);
alter table message add column to_session_id text;
alter table message add column to_role text check (to_role is null or to_role in ('main','subagent','runner'));
alter table message add column issue_id integer references issue(id);
alter table message add column reply_to_id integer references message(id);

create index idx_message_to_session on message(project_id, to_actor, to_session_id);
create index idx_message_to_role on message(project_id, to_role);
create index idx_message_issue on message(issue_id);
create index idx_message_reply_to on message(reply_to_id);
