package sqlstore

// staticEligibleTaskPredicate mirrors only persisted checks in Picker.Pick.
// Runtime availability and admission guards deliberately remain the scheduler's
// responsibility. Keep it in taskListPredicates so pages, counts, facets and
// rollups select the same cohort.
const staticEligibleTaskPredicate = `tasks.status = 'todo'
AND tasks.manual = 0
AND tasks.kind NOT IN ('parent', 'plan', 'issue')
AND (tasks.kind NOT IN ('agent', 'internal')
     OR tasks.agent_profile <> '' OR tasks.launch_profile <> '')
AND NOT EXISTS (
    SELECT 1 FROM task_dependencies td
    LEFT JOIN tasks dep ON dep.id = td.depends_on_task_id
    WHERE td.task_id = tasks.id
      AND (dep.id IS NULL OR dep.status <> 'done')
)`
