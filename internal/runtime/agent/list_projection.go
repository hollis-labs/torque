package agent

import "github.com/hollis-labs/torque/internal/persistence/sqlstore"

// SessionSnapshot preserves the public session projection for paginated store
// reads, including runtime metadata and derived session properties.
func SessionSnapshot(record *sqlstore.SessionRecord) *Session { return sessionFromRecord(record) }
