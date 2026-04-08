package service

import "github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"

// SettingsService provides business logic for application settings.
type SettingsService struct {
	store *sqlstore.Store
}

// Get returns the value for a settings key.
func (s *SettingsService) Get(key string) (string, error) {
	return s.store.GetSetting(key)
}

// Set upserts a settings key/value pair.
func (s *SettingsService) Set(key, value string) error {
	return s.store.SetSetting(key, value)
}

// List returns all settings.
func (s *SettingsService) List() ([]sqlstore.SettingRecord, error) {
	return s.store.ListSettings()
}

// IsFeatureEnabled reports whether the feature flag "features.<feature>" is set to "true".
func (s *SettingsService) IsFeatureEnabled(feature string) bool {
	val, err := s.store.GetSetting("features." + feature)
	if err != nil {
		return false
	}
	return val == "true"
}
