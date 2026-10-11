package sqlstore

// JudgeTask is a task as the judgment evaluation sees it: its text and the
// tags it already carries (the evaluation labels).
type JudgeTask struct {
	ID          string
	Title       string
	Description string
	Tags        []string
}

// JudgeTaskCorpus returns every user-facing task (internal kinds excluded)
// with its tags, in id order. It reads two tables and joins them in memory;
// a few thousand tasks is a few megabytes.
func (s *Store) JudgeTaskCorpus() ([]JudgeTask, error) {
	rows, err := s.ReadDB().Query(`SELECT id, title, description FROM tasks WHERE kind <> 'internal' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []JudgeTask
	index := map[string]int{}
	for rows.Next() {
		var t JudgeTask
		if err := rows.Scan(&t.ID, &t.Title, &t.Description); err != nil {
			return nil, err
		}
		index[t.ID] = len(tasks)
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	trows, err := s.ReadDB().Query(`SELECT task_id, tag_slug FROM task_tags ORDER BY task_id, tag_slug`)
	if err != nil {
		return nil, err
	}
	defer trows.Close()
	for trows.Next() {
		var id, tag string
		if err := trows.Scan(&id, &tag); err != nil {
			return nil, err
		}
		if i, ok := index[id]; ok {
			tasks[i].Tags = append(tasks[i].Tags, tag)
		}
	}
	return tasks, trows.Err()
}
