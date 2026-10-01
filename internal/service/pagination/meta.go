package pagination

// PageMeta is the common metadata for cursor and offset list pages. The
// optional embedded OffsetMeta emits both offset fields in offset mode,
// including next_offset:null on the last page, and neither in cursor mode.
type PageMeta struct {
	Returned   int     `json:"returned"`
	Limit      int     `json:"limit"`
	HasMore    bool    `json:"has_more"`
	NextCursor *string `json:"next_cursor"`
	Total      *int    `json:"total,omitempty"`
	*OffsetMeta
}

type OffsetMeta struct {
	Offset     int  `json:"offset"`
	NextOffset *int `json:"next_offset"`
}

// NewPageMeta takes the emitted page size and the limit+1 probe result.
// total is a whole-cohort count or nil; offset is present only for offset
// traversal. Neither totals nor page fullness determine has_more.
func NewPageMeta(returned, limit int, hasMore bool, nextCursor *string, total *int, offset *int) PageMeta {
	meta := PageMeta{Returned: returned, Limit: limit, HasMore: hasMore, Total: total}
	if hasMore {
		meta.NextCursor = nextCursor
	}
	if offset != nil {
		meta.OffsetMeta = &OffsetMeta{Offset: *offset}
		if hasMore {
			next := *offset + returned
			meta.NextOffset = &next
		}
	}
	return meta
}
