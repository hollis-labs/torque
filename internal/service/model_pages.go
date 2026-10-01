package service

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/hollis-labs/go-modelsdev/modelsdev"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

func ModelCursor(m modelsdev.ModelRef, by string) (string, string) {
	key := m.Name
	switch by {
	case "provider_id":
		key = m.ProviderID
	case "id":
		key = m.ID
	}
	id, _ := json.Marshal([]string{m.ProviderID, m.ID})
	return key, string(id)
}

// Models are catalog-backed. Paging takes one in-memory catalog snapshot;
// the provider/model pair is the stable unique identity across every sort.
func (s *Service) ListModelPage(provider, search string, q ResourceQuery) (ResourcePage, error) {
	n, err := NormalizeResourceQuery("models", q)
	if err != nil {
		return ResourcePage{}, err
	}
	rows := make([]sqlstore.ResourceRow, 0)
	if s.Models != nil {
		for _, m := range s.Models.List() {
			if provider != "" && m.ProviderID != provider {
				continue
			}
			if search != "" && !strings.Contains(strings.ToLower(m.Name+" "+m.ID+" "+m.ProviderID), strings.ToLower(search)) {
				continue
			}
			sv, id := ModelCursor(m, n.SortBy)
			rows = append(rows, sqlstore.ResourceRow{Record: m, SortValue: sv, ID: id})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].SortValue == rows[j].SortValue {
			return rows[i].ID < rows[j].ID
		}
		if n.SortDir == "desc" {
			return rows[i].SortValue > rows[j].SortValue
		}
		return rows[i].SortValue < rows[j].SortValue
	})
	var total *int
	if q.IncludeTotal {
		v := len(rows)
		total = &v
	}
	if n.AfterID != "" {
		filtered := rows[:0]
		for _, row := range rows {
			after := row.SortValue > n.AfterSortValue
			if n.SortDir == "desc" {
				after = row.SortValue < n.AfterSortValue
			}
			if after || (row.SortValue == n.AfterSortValue && row.ID > n.AfterID) {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
	}
	if q.Offset != nil {
		if *q.Offset >= len(rows) {
			rows = rows[:0]
		} else {
			rows = rows[*q.Offset:]
		}
	}
	if len(rows) > n.Limit+1 {
		rows = rows[:n.Limit+1]
	}
	return ResourcePage{Rows: rows, Total: total, Query: n, Offset: q.Offset}, nil
}
