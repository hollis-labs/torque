-- 041 judgments (Postgres override; same shape): the enrichment ledger. One row per question answered about
-- one subject (a task, a planner item) by one version of a question set. It
-- is the audit trail ("nothing silently changed") and the calibration data:
-- what was asked (set, version, hash), what came back (answer, probabilities,
-- confidence), what it would have suggested, who or what accepted it.
--
-- Design:
--   * The ledger stores a hash of the state that was sent, never the text.
--   * Idempotent: the same subject, same set wording (set_hash), same question
--     and same state hash is one row, however many times it is asked.
--   * When a subject's state changes, suggestions still open for the old state
--     are marked 'superseded' (the store does this when a new answer lands).
--   * disposition: suggested -> accepted | rejected | auto_applied (terminal),
--     or superseded. A decided row never changes.

CREATE TABLE IF NOT EXISTS judgments (
    id            BIGSERIAL PRIMARY KEY,
    subject_type  TEXT NOT NULL,                 -- task | planner:<db>
    subject_id    TEXT NOT NULL,
    set_id        TEXT NOT NULL,
    set_version   INTEGER NOT NULL,
    set_hash      TEXT NOT NULL,
    question_key  TEXT NOT NULL,
    answer_type   TEXT NOT NULL,                 -- noul | choice | score
    choice        TEXT NOT NULL DEFAULT '',
    noul          DOUBLE PRECISION,
    score         DOUBLE PRECISION,
    probabilities TEXT NOT NULL DEFAULT '{}',    -- JSON: option key (or level index) -> probability
    confidence    DOUBLE PRECISION NOT NULL DEFAULT 0,
    suggested     TEXT NOT NULL DEFAULT '[]',    -- JSON: tags the answer suggested under the set's mapping
    state_hash    TEXT NOT NULL,
    provider      TEXT NOT NULL DEFAULT '',
    model         TEXT NOT NULL DEFAULT '',      -- the versioned model id that answered
    purpose       TEXT NOT NULL DEFAULT '',
    input_tokens  INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    latency_ms    INTEGER NOT NULL DEFAULT 0,
    disposition   TEXT NOT NULL DEFAULT 'suggested',
    decided_by    TEXT NOT NULL DEFAULT '',
    decided_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE (subject_type, subject_id, set_id, set_hash, question_key, state_hash)
);

CREATE INDEX IF NOT EXISTS idx_judgments_subject ON judgments(subject_type, subject_id);
CREATE INDEX IF NOT EXISTS idx_judgments_set ON judgments(set_id, question_key, disposition);
