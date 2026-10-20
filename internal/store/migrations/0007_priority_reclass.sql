-- Open issues get their priority again by the rule that now picks the
-- default: ideas and observations P3, issues an actor raised to P0 or P1 keep
-- it, and the rest follow the kind of the actor that created them. Closed
-- issues keep theirs. Like 0006, this writes no activity records.

update issue set priority = case
    when type in ('idea', 'observation') then 'P3'
    when priority in ('P0', 'P1') then priority
    when (select kind from actor a where a.id = issue.created_by) = 'user' then 'P1'
    else 'P2'
  end
where status in ('backlog', 'doing', 'review');
