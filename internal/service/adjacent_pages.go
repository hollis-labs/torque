package service

import "github.com/hollis-labs/torque/internal/persistence/sqlstore"

// pageWithTotal only executes a cohort COUNT when explicitly requested. The
// list still overfetches one row for has_more; transports trim before emitting.
func pageWithTotal[T any](include bool, list func() ([]T, error), count func() (int, error)) ([]T, *int, error) {
	items, err := list()
	if err != nil {
		return nil, nil, err
	}
	if !include {
		return items, nil, nil
	}
	total, err := count()
	if err != nil {
		return nil, nil, err
	}
	return items, &total, nil
}

func (s *ProjectService) ListPageWithTotal(f sqlstore.ProjectFilter, include bool) ([]sqlstore.ProjectRecord, *int, error) {
	return pageWithTotal(include,
		func() ([]sqlstore.ProjectRecord, error) { return s.ListPage(f) },
		func() (int, error) { return s.store.CountProjects(f) },
	)
}

func (s *SprintService) ListWithTotal(f sqlstore.SprintFilter, include bool) ([]sqlstore.SprintRecord, *int, error) {
	return pageWithTotal(include,
		func() ([]sqlstore.SprintRecord, error) { return s.List(f) },
		func() (int, error) { return s.store.CountSprints(f) },
	)
}

func epicListFilter(input EpicListInput) sqlstore.EpicFilter {
	return sqlstore.EpicFilter{
		Status:          input.Status,
		ProjectID:       input.ProjectID,
		Search:          input.Search,
		IncludeArchived: input.IncludeArchived,
		Limit:           input.Limit,
		SortBy:          input.SortBy,
		SortDir:         input.SortDir,
		AfterSortValue:  input.AfterSortValue,
		AfterID:         input.AfterID,
	}
}

func (s *EpicService) ListPaginatedWithTotal(input EpicListInput, include bool) ([]sqlstore.EpicRecord, *int, error) {
	return pageWithTotal(include,
		func() ([]sqlstore.EpicRecord, error) { return s.ListPaginated(input) },
		func() (int, error) { return s.store.CountEpics(epicListFilter(input)) },
	)
}

func issueListFilter(input IssueListInput) sqlstore.TaskFilter {
	return sqlstore.TaskFilter{
		Kind:           "issue",
		ProjectID:      input.ProjectID,
		Status:         input.Status,
		Search:         input.Query,
		Limit:          input.Limit,
		SortBy:         input.SortBy,
		SortDir:        input.SortDir,
		AfterSortValue: input.AfterSortValue,
		AfterID:        input.AfterID,
	}
}

func (s *IssueService) ListWithTotal(input IssueListInput, include bool) ([]sqlstore.TaskRecord, *int, error) {
	return pageWithTotal(include,
		func() ([]sqlstore.TaskRecord, error) { return s.List(input) },
		func() (int, error) { return s.store.CountTasks(issueListFilter(input)) },
	)
}

func (s *CommentService) ListFilteredWithTotal(f sqlstore.CommentFilter, include bool) ([]sqlstore.CommentRecord, *int, error) {
	return pageWithTotal(include,
		func() ([]sqlstore.CommentRecord, error) { return s.ListFiltered(f) },
		func() (int, error) { return s.store.CountComments(f) },
	)
}
