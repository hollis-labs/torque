package sqlstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The enrichment ledger (migration 041). See the migration for the design:
// one row per question answered about one subject by one version of a
// question set, idempotent on (subject, set wording, question, state hash),
// stale suggestions superseded, decided rows immutable.

var (
	// ErrJudgmentNotFound wraps a missing judgment id.
	ErrJudgmentNotFound = errors.New("judgment not found")
	// ErrJudgmentDecided is returned when deciding a judgment that is no longer a suggestion.
	ErrJudgmentDecided = errors.New("judgment already decided")
)

// Judgment dispositions.
const (
	JudgmentSuggested   = "suggested"
	JudgmentAccepted    = "accepted"
	JudgmentRejected    = "rejected"
	JudgmentAutoApplied = "auto_applied"
	JudgmentSuperseded  = "superseded"
)

// JudgmentRecord is one ledger row.
type JudgmentRecord struct {
	ID            int64              `json:"id"`
	SubjectType   string             `json:"subject_type"` // task
	SubjectID     string             `json:"subject_id"`
	SetID         string             `json:"set_id"`
	SetVersion    int                `json:"set_version"`
	SetHash       string             `json:"set_hash"`
	QuestionKey   string             `json:"question_key"`
	AnswerType    string             `json:"answer_type"` // noul | choice | score
	Choice        string             `json:"choice,omitempty"`
	Noul          *float64           `json:"noul,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
	Suggested     []string           `json:"suggested"`
	StateHash     string             `json:"state_hash"`
	Provider      string             `json:"provider"`
	Model         string             `json:"model"`
	Purpose       string             `json:"purpose"`
	InputTokens   int                `json:"input_tokens"`
	OutputTokens  int                `json:"output_tokens"`
	LatencyMS     int                `json:"latency_ms"`
	Disposition   string             `json:"disposition"`
	DecidedBy     string             `json:"decided_by,omitempty"`
	DecidedAt     *time.Time         `json:"decided_at,omitempty"`
	CreatedAt     time.Time          `json:"created_at"`
}

// JudgmentFilter narrows ListJudgments; empty fields match everything.
type JudgmentFilter struct {
	SubjectType string
	SubjectID   string
	SetID       string
	QuestionKey string
	Disposition string
	Limit       int // 0 = 200
}

const judgmentCols = `id, subject_type, subject_id, set_id, set_version, set_hash, question_key, answer_type, choice,
	noul, score, probabilities, confidence, suggested, state_hash, provider, model, purpose,
	input_tokens, output_tokens, latency_ms, disposition, decided_by, decided_at, created_at`

// RecordJudgment writes an answer to the ledger. It reports created=false and
// fills r.ID and r.Disposition from the existing row when the same subject,
// set wording, question and state hash were already recorded. When it creates
// a row, suggestions still open for this subject, set and question under an
// older state or older wording are marked superseded, in the same transaction.
func (s *Store) RecordJudgment(r *JudgmentRecord) (created bool, err error) {
	if r.SubjectType == "" || r.SubjectID == "" || r.SetID == "" || r.SetHash == "" || r.QuestionKey == "" || r.AnswerType == "" || r.StateHash == "" {
		return false, errors.New("judgment: subject, set, set hash, question, answer type and state hash are required")
	}
	if r.Disposition == "" {
		r.Disposition = JudgmentSuggested
	}
	probs, _ := json.Marshal(nonNilProbs(r.Probabilities))
	sugg, _ := json.Marshal(nonNilStrings(r.Suggested))

	tx, err := s.beginWriteTx()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(`INSERT OR IGNORE INTO judgments
		(subject_type, subject_id, set_id, set_version, set_hash, question_key, answer_type, choice, noul, score,
		 probabilities, confidence, suggested, state_hash, provider, model, purpose, input_tokens, output_tokens, latency_ms, disposition)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.SubjectType, r.SubjectID, r.SetID, r.SetVersion, r.SetHash, r.QuestionKey, r.AnswerType, r.Choice, r.Noul, r.Score,
		string(probs), r.Confidence, string(sugg), r.StateHash, r.Provider, r.Model, r.Purpose, r.InputTokens, r.OutputTokens, r.LatencyMS, r.Disposition)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		err = tx.QueryRow(`SELECT id, disposition FROM judgments
			WHERE subject_type = ? AND subject_id = ? AND set_id = ? AND set_hash = ? AND question_key = ? AND state_hash = ?`,
			r.SubjectType, r.SubjectID, r.SetID, r.SetHash, r.QuestionKey, r.StateHash).Scan(&r.ID, &r.Disposition)
		return false, err
	}
	if r.ID, err = res.LastInsertId(); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`UPDATE judgments SET disposition = ?
		WHERE subject_type = ? AND subject_id = ? AND set_id = ? AND question_key = ?
		  AND disposition = ? AND id <> ? AND (state_hash <> ? OR set_hash <> ?)`,
		JudgmentSuperseded, r.SubjectType, r.SubjectID, r.SetID, r.QuestionKey, JudgmentSuggested, r.ID, r.StateHash, r.SetHash); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// GetJudgment fetches one row.
func (s *Store) GetJudgment(id int64) (*JudgmentRecord, error) {
	rows, err := s.ReadDB().Query(`SELECT `+judgmentCols+` FROM judgments WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	recs, err := scanJudgments(rows)
	if err != nil {
		return nil, err
	}
	if len(recs) == 0 {
		return nil, fmt.Errorf("%w: %d", ErrJudgmentNotFound, id)
	}
	return &recs[0], nil
}

// ListJudgments returns matching rows, newest first.
func (s *Store) ListJudgments(f JudgmentFilter) ([]JudgmentRecord, error) {
	var where []string
	var args []any
	for col, v := range map[string]string{"subject_type": f.SubjectType, "subject_id": f.SubjectID, "set_id": f.SetID, "question_key": f.QuestionKey, "disposition": f.Disposition} {
		if v != "" {
			where = append(where, col+" = ?")
			args = append(args, v)
		}
	}
	q := `SELECT ` + judgmentCols + ` FROM judgments`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 200
	}
	q += fmt.Sprintf(" ORDER BY id DESC LIMIT %d", limit)
	rows, err := s.ReadDB().Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJudgments(rows)
}

// DecideJudgment moves a suggestion to accepted, rejected or auto_applied,
// recording who decided. A row that is no longer a suggestion is not changed.
func (s *Store) DecideJudgment(id int64, disposition, by string) error {
	switch disposition {
	case JudgmentAccepted, JudgmentRejected, JudgmentAutoApplied:
	default:
		return fmt.Errorf("judgment: cannot decide as %q", disposition)
	}
	res, err := s.db.Exec(`UPDATE judgments SET disposition = ?, decided_by = ?, decided_at = CURRENT_TIMESTAMP
		WHERE id = ? AND disposition = ?`, disposition, by, id, JudgmentSuggested)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var cur string
		switch err := s.db.QueryRow(`SELECT disposition FROM judgments WHERE id = ?`, id).Scan(&cur); {
		case err == sql.ErrNoRows:
			return fmt.Errorf("%w: %d", ErrJudgmentNotFound, id)
		case err != nil:
			return err
		}
		return fmt.Errorf("%w: %d is %s", ErrJudgmentDecided, id, cur)
	}
	return nil
}

// JudgmentCounts returns, for a set, the rows per question and disposition:
// the acceptance rate of a set is how it earns its place.
func (s *Store) JudgmentCounts(setID string) (map[string]map[string]int, error) {
	rows, err := s.ReadDB().Query(`SELECT question_key, disposition, COUNT(*) FROM judgments WHERE set_id = ? GROUP BY question_key, disposition`, setID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]int{}
	for rows.Next() {
		var q, d string
		var n int
		if err := rows.Scan(&q, &d, &n); err != nil {
			return nil, err
		}
		if out[q] == nil {
			out[q] = map[string]int{}
		}
		out[q][d] = n
	}
	return out, rows.Err()
}

func scanJudgments(rows *sql.Rows) ([]JudgmentRecord, error) {
	var out []JudgmentRecord
	for rows.Next() {
		var r JudgmentRecord
		var noul, score sql.NullFloat64
		var probs, sugg string
		var decided sql.NullTime
		if err := rows.Scan(&r.ID, &r.SubjectType, &r.SubjectID, &r.SetID, &r.SetVersion, &r.SetHash, &r.QuestionKey, &r.AnswerType, &r.Choice,
			&noul, &score, &probs, &r.Confidence, &sugg, &r.StateHash, &r.Provider, &r.Model, &r.Purpose,
			&r.InputTokens, &r.OutputTokens, &r.LatencyMS, &r.Disposition, &r.DecidedBy, &decided, &r.CreatedAt); err != nil {
			return nil, err
		}
		if noul.Valid {
			v := noul.Float64
			r.Noul = &v
		}
		if score.Valid {
			v := score.Float64
			r.Score = &v
		}
		if decided.Valid {
			t := decided.Time
			r.DecidedAt = &t
		}
		_ = json.Unmarshal([]byte(probs), &r.Probabilities)
		_ = json.Unmarshal([]byte(sugg), &r.Suggested)
		out = append(out, r)
	}
	return out, rows.Err()
}

func nonNilProbs(m map[string]float64) map[string]float64 {
	if m == nil {
		return map[string]float64{}
	}
	return m
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
