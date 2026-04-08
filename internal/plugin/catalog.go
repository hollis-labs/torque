package plugin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
)

// CatalogEntry represents a single plugin in the catalog.
type CatalogEntry struct {
	ID          string `json:"id" yaml:"id"`
	Name        string `json:"name" yaml:"name"`
	Version     string `json:"version" yaml:"version"`
	Description string `json:"description" yaml:"description"`
	Author      string `json:"author" yaml:"author"`
	URL         string `json:"url" yaml:"url"`
	Checksum    string `json:"checksum" yaml:"checksum"`
	Signature   string `json:"signature,omitempty" yaml:"signature,omitempty"`
	MinVersion  string `json:"min_version,omitempty" yaml:"min_version,omitempty"`
}

// Catalog is a collection of plugin entries.
type Catalog struct {
	Version string         `json:"version" yaml:"version"`
	Entries []CatalogEntry `json:"entries" yaml:"entries"`
}

// CatalogSource defines where to fetch a catalog from.
type CatalogSource struct {
	Name     string `yaml:"name"`
	URL      string `yaml:"url"`      // HTTP URL or file:// path
	Priority int    `yaml:"priority"` // lower = higher priority
	Trusted  bool   `yaml:"trusted"`  // skip signature verification
}

// HTTPClient abstracts HTTP fetching for testability.
type HTTPClient interface {
	Get(url string) ([]byte, error)
}

// defaultHTTPClient implements HTTPClient using net/http.
type defaultHTTPClient struct{}

func (c *defaultHTTPClient) Get(url string) ([]byte, error) {
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("http get %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http get %s: status %d", url, resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	return data, nil
}

// CatalogFetcher fetches and merges catalogs from multiple sources.
type CatalogFetcher struct {
	sources []CatalogSource
	client  HTTPClient
}

// NewCatalogFetcher creates a new CatalogFetcher with the given sources.
func NewCatalogFetcher(sources []CatalogSource) *CatalogFetcher {
	return &CatalogFetcher{
		sources: sources,
		client:  &defaultHTTPClient{},
	}
}

// SetHTTPClient sets a custom HTTP client for testing.
func (f *CatalogFetcher) SetHTTPClient(client HTTPClient) {
	f.client = client
}

// Fetch fetches all sources, handles file:// URLs, and merges catalogs.
// Sources are sorted by priority (lower = higher priority) before merging.
func (f *CatalogFetcher) Fetch() (*Catalog, error) {
	// Sort sources by priority ascending (lower priority number = higher priority).
	sorted := make([]CatalogSource, len(f.sources))
	copy(sorted, f.sources)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Priority < sorted[j].Priority
	})

	var catalogs []*Catalog
	for _, src := range sorted {
		cat, err := f.fetchOne(src)
		if err != nil {
			return nil, fmt.Errorf("fetch catalog %q: %w", src.Name, err)
		}
		catalogs = append(catalogs, cat)
	}

	return MergeCatalogs(catalogs...), nil
}

func (f *CatalogFetcher) fetchOne(src CatalogSource) (*Catalog, error) {
	var data []byte
	var err error

	if strings.HasPrefix(src.URL, "file://") {
		path := strings.TrimPrefix(src.URL, "file://")
		data, err = os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read file %s: %w", path, err)
		}
	} else {
		data, err = f.client.Get(src.URL)
		if err != nil {
			return nil, err
		}
	}

	var cat Catalog
	if err := json.Unmarshal(data, &cat); err != nil {
		return nil, fmt.Errorf("parse catalog JSON: %w", err)
	}
	return &cat, nil
}

// MergeCatalogs merges multiple catalogs into one.
// Earlier catalogs (higher priority) win for duplicate plugin IDs.
func MergeCatalogs(catalogs ...*Catalog) *Catalog {
	merged := &Catalog{}
	seen := make(map[string]bool)

	for _, cat := range catalogs {
		if cat == nil {
			continue
		}
		if merged.Version == "" && cat.Version != "" {
			merged.Version = cat.Version
		}
		for _, entry := range cat.Entries {
			if !seen[entry.ID] {
				seen[entry.ID] = true
				merged.Entries = append(merged.Entries, entry)
			}
		}
	}

	if merged.Entries == nil {
		merged.Entries = []CatalogEntry{}
	}

	return merged
}

// Find returns the catalog entry with the given ID, or nil if not found.
func (c *Catalog) Find(id string) *CatalogEntry {
	for i := range c.Entries {
		if c.Entries[i].ID == id {
			return &c.Entries[i]
		}
	}
	return nil
}

// Search returns all entries whose name or description contains the query (case-insensitive).
func (c *Catalog) Search(query string) []CatalogEntry {
	q := strings.ToLower(query)
	var results []CatalogEntry
	for _, entry := range c.Entries {
		if strings.Contains(strings.ToLower(entry.Name), q) ||
			strings.Contains(strings.ToLower(entry.Description), q) {
			results = append(results, entry)
		}
	}
	if results == nil {
		return []CatalogEntry{}
	}
	return results
}
