package service

import (
	"fmt"
	"time"
)

// MaxRunTimeSeriesBuckets bounds the requested output window, not run rows.
// Oversized windows reject rather than silently discarding older activity.
const MaxRunTimeSeriesBuckets = 200

type RunTimeSeriesQuery struct {
	RunQuery
	Bucket          string
	TZOffsetMinutes int
}
type RunTimeSeriesTotals struct {
	Count            int     `json:"count"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	Cost             float64 `json:"cost"`
}
type RunTimeSeriesBucket struct {
	Start time.Time `json:"start"`
	RunTimeSeriesTotals
	StatusCounts map[string]int `json:"status_counts"`
}
type RunTimeSeriesResult struct {
	Bucket          string                `json:"bucket"`
	TZOffsetMinutes int                   `json:"tz_offset_minutes"`
	Since           time.Time             `json:"since"`
	Until           time.Time             `json:"until"`
	Buckets         []RunTimeSeriesBucket `json:"buckets"`
	Totals          RunTimeSeriesTotals   `json:"totals"`
}

func (s *RunService) TimeSeries(q RunTimeSeriesQuery) (RunTimeSeriesResult, error) {
	invalid := func(field, message string) (RunTimeSeriesResult, error) {
		return RunTimeSeriesResult{}, &ValidationError{Field: field, Message: message}
	}
	for _, p := range []struct {
		field string
		set   bool
	}{
		{"limit", q.Limit != 0}, {"offset", q.OffsetSet || q.Offset != 0}, {"cursor", q.Cursor != ""}, {"sort_by", q.SortBy != ""}, {"sort_dir", q.SortDir != ""}, {"include_total", q.IncludeTotal},
	} {
		if p.set {
			return invalid(p.field, p.field+" is not supported by time-series aggregates")
		}
	}
	if q.Bucket == "" {
		q.Bucket = "day"
	}
	if q.Bucket != "day" && q.Bucket != "hour" {
		return invalid("bucket", "bucket must be hour or day")
	}
	if q.TZOffsetMinutes < -840 || q.TZOffsetMinutes > 840 {
		return invalid("tz_offset_minutes", "tz_offset_minutes must be between -840 and 840")
	}
	f, _, _, _, err := NormalizeRunQuery(q.RunQuery)
	if err != nil {
		return RunTimeSeriesResult{}, err
	}
	for _, bound := range []struct {
		field string
		value time.Time
	}{{"since", f.Since}, {"until", f.Until}} {
		if bound.value.IsZero() {
			return invalid(bound.field, bound.field+" is required")
		}
		if bound.value.Year() < 1 || bound.value.Year() > 9999 {
			return invalid(bound.field, bound.field+" must be in years 1 through 9999")
		}
	}
	zone := time.FixedZone("", q.TZOffsetMinutes*60)
	for _, bound := range []struct {
		field string
		value time.Time
	}{{"since", f.Since}, {"until", f.Until}} {
		if year := bound.value.In(zone).Year(); year < 1 || year > 9999 {
			return invalid(bound.field, bound.field+" with tz_offset_minutes must be in years 1 through 9999")
		}
	}
	floor := func(t time.Time) time.Time {
		t = t.In(zone)
		hour := 0
		if q.Bucket == "hour" {
			hour = t.Hour()
		}
		return time.Date(t.Year(), t.Month(), t.Day(), hour, 0, 0, 0, zone)
	}
	first, last := floor(f.Since), floor(f.Until)
	step := 24 * time.Hour
	if q.Bucket == "hour" {
		step = time.Hour
	}
	if !last.Before(first.Add(step * MaxRunTimeSeriesBuckets)) {
		return invalid("until", fmt.Sprintf("since/until window exceeds %d %s buckets; shorten the window or use bucket=day", MaxRunTimeSeriesBuckets, q.Bucket))
	}
	result := RunTimeSeriesResult{Bucket: q.Bucket, TZOffsetMinutes: q.TZOffsetMinutes, Since: f.Since, Until: f.Until, Buckets: []RunTimeSeriesBucket{}}
	indexes := map[string]int{}
	for start := first; !start.After(last); start = start.Add(step) {
		indexes[start.Format("2006-01-02T15:04:05")] = len(result.Buckets)
		result.Buckets = append(result.Buckets, RunTimeSeriesBucket{Start: start.UTC(), StatusCounts: map[string]int{}})
	}
	cells, err := s.store.RunTimeSeries(f, q.Bucket, q.TZOffsetMinutes)
	if err != nil {
		return RunTimeSeriesResult{}, err
	}
	for _, cell := range cells {
		i, ok := indexes[cell.Start]
		if !ok {
			return RunTimeSeriesResult{}, fmt.Errorf("time-series bucket outside requested window: %q", cell.Start)
		}
		b := &result.Buckets[i]
		b.Count += cell.Count
		b.PromptTokens += cell.PromptTokens
		b.CompletionTokens += cell.CompletionTokens
		b.Cost += cell.Cost
		b.StatusCounts[cell.Status] += cell.Count
		result.Totals.Count += cell.Count
		result.Totals.PromptTokens += cell.PromptTokens
		result.Totals.CompletionTokens += cell.CompletionTokens
		result.Totals.Cost += cell.Cost
	}
	return result, nil
}
