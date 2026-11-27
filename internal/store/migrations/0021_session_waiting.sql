-- Waiting state of an agent session: approval, user input, or external wait.
-- Existing rows start with all three columns NULL.
alter table session add column state text check (state is null or state in ('waiting_approval', 'waiting_input', 'waiting_external'));
alter table session add column state_reason text;
alter table session add column state_at text;
create index session_waiting on session(state_at) where state is not null;
