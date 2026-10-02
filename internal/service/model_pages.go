package service

import (
	"cmp"
	"encoding/json"
	"math"
	"sort"
	"strconv"
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
	case "cost":
		key = strconv.FormatFloat(m.Cost.Input, 'g', -1, 64)
	case "context":
		key = strconv.Itoa(m.Limit.ContextWindow)
	case "output":
		key = strconv.Itoa(m.Limit.MaxOutputTokens)
	}
	id, _ := json.Marshal([]string{m.ProviderID, m.ID})
	return key, string(id)
}

// modelSortCompare is used for both ordering and cursor continuation.
// Catalog value structs have no presence flags: free and unknown both use zero.
func modelSortCompare(by, dir, a, b string) int {
	result := cmp.Compare(a, b)
	switch by {
	case "cost":
		av, _ := strconv.ParseFloat(a, 64)
		bv, _ := strconv.ParseFloat(b, 64)
		result = cmp.Compare(av, bv)
	case "context", "output":
		av, _ := strconv.ParseInt(a, 10, 64)
		bv, _ := strconv.ParseInt(b, 10, 64)
		result = cmp.Compare(av, bv)
	}
	if dir == "desc" {
		return -result
	}
	return result
}

func validateModelSortCursor(by, value string) error {
	switch by {
	case "cost":
		v, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return &ValidationError{Field: "cursor", Message: "invalid model cost cursor"}
		}
	case "context", "output":
		if _, err := strconv.ParseInt(value, 10, 64); err != nil {
			return &ValidationError{Field: "cursor", Message: "invalid model limit cursor"}
		}
	}
	return nil
}

// Models are catalog-backed. Paging takes one in-memory catalog snapshot;
// the provider/model pair is the stable unique identity across every sort.
func (s *Service) ListModelPage(provider, search string, q ResourceQuery) (ResourcePage, error) {
	n, err := NormalizeResourceQuery("models", q)
	if err != nil {
		return ResourcePage{}, err
	}
	if n.AfterID != "" {
		if err := validateModelSortCursor(n.SortBy, n.AfterSortValue); err != nil {
			return ResourcePage{}, err
		}
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
		order := modelSortCompare(n.SortBy, n.SortDir, rows[i].SortValue, rows[j].SortValue)
		if order == 0 {
			return rows[i].ID < rows[j].ID
		}
		return order < 0
	})
	var total *int
	if q.IncludeTotal {
		v := len(rows)
		total = &v
	}
	if n.AfterID != "" {
		filtered := rows[:0]
		for _, row := range rows {
			order := modelSortCompare(n.SortBy, n.SortDir, row.SortValue, n.AfterSortValue)
			if order > 0 || (order == 0 && row.ID > n.AfterID) {
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
