package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/hollis-labs/torque/internal/config"
)

// loadProfilesOrEmpty resolves the canonical profiles.yaml location and
// returns a live source object. Missing files are not an error: mock-only/dev
// setups can still boot and real task dispatch will fail later at Validate
// time.
func loadProfilesOrEmpty(cfg *config.Config) (*config.ReloadableProfiles, error) {
	path, err := resolveProfilesPath(cfg)
	if err != nil {
		return nil, fmt.Errorf("resolve profiles path: %w", err)
	}

	profiles := config.ProfileMap{}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		log.Printf("no profiles file at %s (using empty profile map)", path)
		return config.NewReloadableProfiles(path, profiles), nil
	}

	profiles, err = config.LoadProfiles(path)
	if err != nil {
		log.Printf("failed to load profiles from %s: %v (using empty profile map)", path, err)
		profiles = config.ProfileMap{}
	} else {
		log.Printf("loaded %d profile(s) from %s", len(profiles), path)
	}
	return config.NewReloadableProfiles(path, profiles), nil
}

// resolveProfilesPath returns the profiles.yaml path: an explicit
// TORQUE_PROFILES_PATH wins, otherwise cfg.ProfilesPath
// (<ConfigDir>/profiles.yaml, resolved via go-apppaths in config.Load).
func resolveProfilesPath(cfg *config.Config) (string, error) {
	if raw := os.Getenv("TORQUE_PROFILES_PATH"); raw != "" {
		return filepath.Abs(raw)
	}
	if cfg == nil {
		return "", fmt.Errorf("config is nil")
	}
	if cfg.ProfilesPath == "" {
		return "", fmt.Errorf("config has no profiles path")
	}
	return filepath.Abs(cfg.ProfilesPath)
}

func watchProfiles(ctx context.Context, profiles *config.ReloadableProfiles, interval time.Duration) {
	if profiles == nil {
		return
	}
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var lastSeen []byte
	path := profiles.Path()
	for {
		current, exists, err := readProfileFile(path)
		if err != nil {
			log.Printf("[profiles] watch read %s: %v", path, err)
		} else if !bytes.Equal(current, lastSeen) {
			switch {
			case !exists:
				profiles.Store(config.ProfileMap{})
				log.Printf("[profiles] reloaded %s: file removed, using empty profile map", path)
			default:
				loaded, loadErr := config.LoadProfiles(path)
				if loadErr != nil {
					log.Printf("[profiles] reload failed for %s: %v (keeping prior snapshot)", path, loadErr)
				} else {
					profiles.Store(loaded)
					log.Printf("[profiles] reloaded %d profile(s) from %s", len(loaded), path)
				}
			}
			lastSeen = current
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func readProfileFile(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}
