package sqlstore

import (
	"database/sql"
)

// SettingRecord mirrors the settings table row.
type SettingRecord struct {
	Key   string
	Value string
}

// GetSetting returns the value for a key, or "" if not found.
func (s *Store) GetSetting(key string) (string, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return value, nil
}

// SetSetting upserts a key/value pair.
func (s *Store) SetSetting(key, value string) error {
	const q = `INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`
	_, err := s.db.Exec(q, key, value)
	return err
}

// ListSettings returns all settings ordered by key.
func (s *Store) ListSettings() ([]SettingRecord, error) {
	rows, err := s.db.Query(`SELECT key, value FROM settings ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var settings []SettingRecord
	for rows.Next() {
		var r SettingRecord
		if err := rows.Scan(&r.Key, &r.Value); err != nil {
			return nil, err
		}
		settings = append(settings, r)
	}
	return settings, rows.Err()
}
