package service

import (
	"context"
	"encoding/json"
	"github.com/hollis-labs/torque/internal/tetherclient"
)

// ForumsService owns the Torque feature gate, not forum data or attribution.
type ForumsService struct {
	feature *FeatureService
	Client  *tetherclient.ForumsClient
}

func (s *ForumsService) Do(ctx context.Context, method, path string, body json.RawMessage) (json.RawMessage, int, error) {
	if err := s.feature.Require("forums"); err != nil {
		return nil, 0, err
	}
	return s.Client.Do(ctx, method, path, body)
}
