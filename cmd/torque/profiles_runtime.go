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

// loadProfilesOrEmpty resolves the canonical profiles.yaml location,
// reconciles the legacy clockwork path onto it when applicable, and returns a
// live source object. Missing files are not an error: mock-only/dev setups can
// still boot and real task dispatch will fail later at Validate time.
func loadProfilesOrEmpty(cfg *config.Config) (*config.ReloadableProfiles, error) {
	path, err := resolveProfilesPath(cfg)
	if err != nil {
		return nil, fmt.Errorf("resolve profiles path: %w", err)
	}
	if err := reconcileLegacyProfilesPath(path); err != nil {
		return nil, fmt.Errorf("reconcile profiles path: %w", err)
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

func resolveProfilesPath(cfg *config.Config) (string, error) {
	if raw := os.Getenv("TORQUE_PROFILES_PATH"); raw != "" {
		return filepath.Abs(raw)
	}
	if cfg == nil {
		return "", fmt.Errorf("config is nil")
	}
	return filepath.Abs(filepath.Join(cfg.DataDir, "profiles.yaml"))
}

func reconcileLegacyProfilesPath(canonicalPath string) error {
	legacyPath, ok := legacyProfilesPathForCanonical(canonicalPath)
	if !ok || legacyPath == canonicalPath {
		return nil
	}

	canonInfo, canonErr := os.Lstat(canonicalPath)
	legacyInfo, legacyErr := os.Lstat(legacyPath)

	switch {
	case errors.Is(canonErr, os.ErrNotExist) && errors.Is(legacyErr, os.ErrNotExist):
		return nil
	case errors.Is(canonErr, os.ErrNotExist):
		if err := ensureParentDir(canonicalPath); err != nil {
			return err
		}
		if err := copyFile(canonicalPath, legacyPath, fileModeOrDefault(legacyInfo)); err != nil {
			return err
		}
		log.Printf("[profiles] migrated legacy clockwork profiles to canonical path %s", canonicalPath)
	case canonErr == nil && legacyErr == nil:
		canonBytes, err := os.ReadFile(canonicalPath)
		if err != nil {
			return err
		}
		legacyBytes, err := os.ReadFile(legacyPath)
		if err != nil {
			return err
		}
		if !bytes.Equal(canonBytes, legacyBytes) {
			// Preserve whichever copy was edited most recently, but always land
			// the winning contents at the canonical torque path.
			if legacyInfo.ModTime().After(canonInfo.ModTime()) {
				if err := os.WriteFile(canonicalPath, legacyBytes, fileModeOrDefault(canonInfo)); err != nil {
					return err
				}
				log.Printf("[profiles] reconciled divergent legacy profiles by promoting newer %s to %s", legacyPath, canonicalPath)
			} else {
				log.Printf("[profiles] reconciled divergent legacy profiles by keeping newer canonical copy at %s", canonicalPath)
			}
		}
	case canonErr == nil && errors.Is(legacyErr, os.ErrNotExist):
		// Fall through to symlink creation below.
	default:
		if canonErr != nil {
			return canonErr
		}
		if legacyErr != nil {
			return legacyErr
		}
	}

	return ensureCompatSymlink(legacyPath, canonicalPath)
}

func legacyProfilesPathForCanonical(canonicalPath string) (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	torqueRoot := filepath.Join(home, ".torque")
	rel, err := filepath.Rel(torqueRoot, canonicalPath)
	if err != nil || rel == ".." || len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator) {
		return "", false
	}
	return filepath.Join(home, ".clockwork", rel), true
}

func ensureCompatSymlink(legacyPath, canonicalPath string) error {
	if legacyPath == "" {
		return nil
	}
	if err := ensureParentDir(legacyPath); err != nil {
		return err
	}
	if target, err := os.Readlink(legacyPath); err == nil {
		if filepath.Clean(target) == filepath.Clean(canonicalPath) {
			return nil
		}
	}
	if err := os.RemoveAll(legacyPath); err != nil {
		return err
	}
	if err := os.Symlink(canonicalPath, legacyPath); err != nil {
		return err
	}
	log.Printf("[profiles] compatibility path %s -> %s", legacyPath, canonicalPath)
	return nil
}

func ensureParentDir(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o755)
}

func copyFile(dst, src string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, mode)
}

func fileModeOrDefault(info os.FileInfo) os.FileMode {
	if info == nil {
		return 0o644
	}
	return info.Mode().Perm()
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
