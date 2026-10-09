package messaging

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	gomsg "github.com/hollis-labs/substrate/mesh/messaging"
	"github.com/hollis-labs/torque/internal/service/pagination"
)

type PageQueryError struct{ Field, Message string }

func (e *PageQueryError) Error() string { return e.Message }

func ParsePageQuery(values url.Values, drain bool) (PageQuery, error) {
	q := PageQuery{Limit: pagination.DefaultLimit, SortDir: "asc"}
	fail := func(field, message string) (PageQuery, error) { return q, &PageQueryError{field, message} }
	allowed := map[string]bool{"to": true, "kind": true, "channel": true, "thread_id": true, "limit": true, "sort_by": true, "sort_dir": true, "cursor": true, "offset": true, "include_total": true}
	for k, v := range values {
		if !allowed[k] {
			return fail(k, "unknown query parameter: "+k)
		}
		if k != "kind" && k != "channel" && len(v) > 1 {
			return fail(k, "query parameter cannot be repeated: "+k)
		}
	}
	if _, ok := values["limit"]; ok {
		v, err := strconv.Atoi(values.Get("limit"))
		if err != nil || v < 0 {
			return fail("limit", "limit must be a non-negative integer")
		}
		if v > 0 {
			q.Limit = v
		}
		if q.Limit > pagination.MaxLimit {
			q.Limit = pagination.MaxLimit
		}
	}
	if raw := values.Get("sort_by"); raw != "" && raw != "created_at" {
		return fail("sort_by", "sort_by must be created_at")
	}
	if raw := values.Get("sort_dir"); raw != "" {
		dir, err := pagination.ValidateSortDir(raw)
		if err != nil {
			return fail("sort_dir", err.Error())
		}
		q.SortDir = dir
	}
	if _, ok := values["include_total"]; ok {
		switch strings.ToLower(values.Get("include_total")) {
		case "true", "1", "yes":
			q.IncludeTotal = true
		case "false", "0", "no", "":
		default:
			return fail("include_total", "include_total must be a boolean")
		}
	}
	if raw := values.Get("cursor"); raw != "" {
		c, err := pagination.Decode(raw)
		if err != nil {
			return fail("cursor", err.Error())
		}
		if err = c.Validate("created_at", q.SortDir); err != nil {
			return fail("cursor", err.Error())
		}
		q.AfterTime, err = time.Parse(time.RFC3339Nano, c.SortValue)
		if err != nil {
			return fail("cursor", "invalid message timestamp")
		}
		q.AfterID = c.ID
	}
	if _, ok := values["offset"]; ok {
		v, err := strconv.Atoi(values.Get("offset"))
		if err != nil || v < 0 {
			return fail("offset", "offset must be a non-negative integer")
		}
		q.Offset = &v
		if q.AfterID != "" {
			return fail("cursor", "cursor and offset are mutually exclusive")
		}
	}
	if drain {
		if q.AfterID != "" {
			return fail("cursor", "inbox drain has no cursor; call again for the next batch")
		}
		if q.Offset != nil {
			return fail("offset", "inbox drain has no offset; call again for the next batch")
		}
		if q.SortDir != "asc" {
			return fail("sort_dir", "inbox drain preserves created_at asc delivery order")
		}
	}
	return q, nil
}

type MessagePageMeta struct {
	pagination.PageMeta
	SortBy           string `json:"sort_by"`
	SortDir          string `json:"sort_dir"`
	TotalUnavailable string `json:"total_unavailable,omitempty"`
}
type MessagePageEnvelope struct {
	Items []gomsg.Envelope `json:"items"`
	Meta  MessagePageMeta  `json:"meta"`
}

func MessageEnvelope(page MessagePage, q PageQuery, drain bool) MessagePageEnvelope {
	items := page.Items
	if items == nil {
		items = []gomsg.Envelope{}
	}
	more := page.HasMore || len(items) > q.Limit
	if len(items) > q.Limit {
		items = items[:q.Limit]
	}
	var next *string
	if more && len(items) > 0 && !drain {
		last := items[len(items)-1]
		s := pagination.Encode("created_at", q.SortDir, last.CreatedAt.UTC().Format(time.RFC3339Nano), last.ID)
		next = &s
	}
	return MessagePageEnvelope{items, MessagePageMeta{pagination.NewPageMeta(len(items), q.Limit, more, next, page.Total, q.Offset), "created_at", q.SortDir, page.TotalUnavailable}}
}

func (s *HTTPStore) DrainInboxPage(context.Context, gomsg.Address, gomsg.Filter, PageQuery) (MessagePage, error) {
	return MessagePage{}, fmt.Errorf("inbox is never federated")
}
