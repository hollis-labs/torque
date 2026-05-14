package service

import "github.com/hollis-labs/torque/internal/persistence/sqlstore"

// knownFeatures lists all valid opt-in feature names.
var knownFeatures = map[string]bool{
	"sprints":     true,
	"projects":    true,
	"epics":       true,
	"collections": true,
}

// FeatureService controls opt-in feature flags stored in the settings table.
type FeatureService struct {
	store *sqlstore.Store
}

// IsEnabled checks whether a feature flag is set to "true" in settings.
func (s *FeatureService) IsEnabled(feature string) bool {
	val, err := s.store.GetSetting("features." + feature)
	if err != nil || val == "" {
		return false
	}
	return val == "true"
}

// Enable turns on a feature flag. Returns ValidationError for unknown features.
func (s *FeatureService) Enable(feature string) error {
	if !knownFeatures[feature] {
		return &ValidationError{Field: "feature", Message: "unknown feature: " + feature}
	}
	return s.store.SetSetting("features."+feature, "true")
}

// Disable turns off a feature flag. Returns ValidationError for unknown features.
func (s *FeatureService) Disable(feature string) error {
	if !knownFeatures[feature] {
		return &ValidationError{Field: "feature", Message: "unknown feature: " + feature}
	}
	return s.store.SetSetting("features."+feature, "false")
}

// ListEnabled returns the names of all currently enabled features.
func (s *FeatureService) ListEnabled() []string {
	var enabled []string
	for f := range knownFeatures {
		if s.IsEnabled(f) {
			enabled = append(enabled, f)
		}
	}
	return enabled
}

// Require returns a FeatureDisabledError if the feature is not enabled.
// Use this as a guard at the top of service methods that require a feature.
func (s *FeatureService) Require(feature string) error {
	if !s.IsEnabled(feature) {
		return &FeatureDisabledError{Feature: feature}
	}
	return nil
}
