package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockHTTPClient is an HTTPClient that returns pre-set data.
type mockHTTPClient struct {
	responses map[string][]byte
	errors    map[string]error
}

func (m *mockHTTPClient) Get(url string) ([]byte, error) {
	if err, ok := m.errors[url]; ok {
		return nil, err
	}
	if data, ok := m.responses[url]; ok {
		return data, nil
	}
	return nil, fmt.Errorf("no mock response for %s", url)
}

func makeCatalog(version string, entries ...CatalogEntry) *Catalog {
	return &Catalog{Version: version, Entries: entries}
}

func makeEntry(id, name, description string) CatalogEntry {
	return CatalogEntry{ID: id, Name: name, Description: description}
}

func TestCatalogFind(t *testing.T) {
	cat := makeCatalog("1.0",
		makeEntry("plugin-a", "Plugin A", "first plugin"),
		makeEntry("plugin-b", "Plugin B", "second plugin"),
	)

	entry := cat.Find("plugin-a")
	require.NotNil(t, entry)
	assert.Equal(t, "plugin-a", entry.ID)
	assert.Equal(t, "Plugin A", entry.Name)

	missing := cat.Find("plugin-z")
	assert.Nil(t, missing)
}

func TestCatalogSearch(t *testing.T) {
	cat := makeCatalog("1.0",
		makeEntry("plugin-a", "Alpha Tool", "does alpha things"),
		makeEntry("plugin-b", "Beta Helper", "helps with beta"),
		makeEntry("plugin-c", "Gamma Widget", "totally unrelated"),
	)

	// Search by name substring.
	results := cat.Search("alpha")
	require.Len(t, results, 1)
	assert.Equal(t, "plugin-a", results[0].ID)

	// Case insensitive.
	results = cat.Search("BETA")
	require.Len(t, results, 1)
	assert.Equal(t, "plugin-b", results[0].ID)

	// Search in description.
	results = cat.Search("alpha things")
	require.Len(t, results, 1)
	assert.Equal(t, "plugin-a", results[0].ID)

	// No match.
	results = cat.Search("zzznomatch")
	assert.Empty(t, results)
}

func TestMergeCatalogs(t *testing.T) {
	// Higher priority (first) wins for same ID.
	cat1 := makeCatalog("1.0",
		makeEntry("plugin-a", "Plugin A v1", "from source 1"),
		makeEntry("plugin-b", "Plugin B", "only in source 1"),
	)
	cat2 := makeCatalog("2.0",
		makeEntry("plugin-a", "Plugin A v2", "from source 2"),
		makeEntry("plugin-c", "Plugin C", "only in source 2"),
	)

	merged := MergeCatalogs(cat1, cat2)
	assert.Equal(t, "1.0", merged.Version)
	assert.Len(t, merged.Entries, 3)

	entry := merged.Find("plugin-a")
	require.NotNil(t, entry)
	assert.Equal(t, "Plugin A v1", entry.Name) // cat1 wins

	assert.NotNil(t, merged.Find("plugin-b"))
	assert.NotNil(t, merged.Find("plugin-c"))
}

func TestMergeCatalogs_Empty(t *testing.T) {
	merged := MergeCatalogs()
	assert.NotNil(t, merged)
	assert.Empty(t, merged.Entries)

	merged2 := MergeCatalogs(nil, nil)
	assert.NotNil(t, merged2)
	assert.Empty(t, merged2.Entries)

	cat := makeCatalog("1.0", makeEntry("plugin-a", "A", "desc"))
	merged3 := MergeCatalogs(nil, cat, nil)
	assert.Len(t, merged3.Entries, 1)
}

func TestCatalogFetcher_FileSource(t *testing.T) {
	cat := makeCatalog("1.0",
		makeEntry("plugin-a", "Plugin A", "a plugin"),
	)
	data, err := json.Marshal(cat)
	require.NoError(t, err)

	f, err := os.CreateTemp(t.TempDir(), "catalog-*.json")
	require.NoError(t, err)
	_, err = f.Write(data)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	fetcher := NewCatalogFetcher([]CatalogSource{
		{Name: "local", URL: "file://" + f.Name(), Priority: 0},
	})

	result, err := fetcher.Fetch()
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.Entries, 1)
	assert.Equal(t, "plugin-a", result.Entries[0].ID)
}

func TestCatalogFetcher_HTTPSource(t *testing.T) {
	cat := makeCatalog("1.0",
		makeEntry("plugin-http", "HTTP Plugin", "fetched via http"),
	)
	data, err := json.Marshal(cat)
	require.NoError(t, err)

	mockClient := &mockHTTPClient{
		responses: map[string][]byte{
			"http://example.com/catalog.json": data,
		},
	}

	fetcher := NewCatalogFetcher([]CatalogSource{
		{Name: "remote", URL: "http://example.com/catalog.json", Priority: 0},
	})
	fetcher.SetHTTPClient(mockClient)

	result, err := fetcher.Fetch()
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.Entries, 1)
	assert.Equal(t, "plugin-http", result.Entries[0].ID)
}

func TestCatalogFetcher_MultipleSourcesMerge(t *testing.T) {
	// source1 has higher priority (lower number), wins for plugin-shared.
	cat1 := makeCatalog("1.0",
		makeEntry("plugin-shared", "Shared v1", "from source 1"),
		makeEntry("plugin-only1", "Only in 1", "exclusive"),
	)
	cat2 := makeCatalog("2.0",
		makeEntry("plugin-shared", "Shared v2", "from source 2"),
		makeEntry("plugin-only2", "Only in 2", "exclusive"),
	)

	data1, err := json.Marshal(cat1)
	require.NoError(t, err)
	data2, err := json.Marshal(cat2)
	require.NoError(t, err)

	mockClient := &mockHTTPClient{
		responses: map[string][]byte{
			"http://source1.example.com/catalog.json": data1,
			"http://source2.example.com/catalog.json": data2,
		},
	}

	fetcher := NewCatalogFetcher([]CatalogSource{
		{Name: "source1", URL: "http://source1.example.com/catalog.json", Priority: 1},
		{Name: "source2", URL: "http://source2.example.com/catalog.json", Priority: 2},
	})
	fetcher.SetHTTPClient(mockClient)

	result, err := fetcher.Fetch()
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.Entries, 3)

	shared := result.Find("plugin-shared")
	require.NotNil(t, shared)
	assert.Equal(t, "Shared v1", shared.Name) // source1 wins (priority 1 < 2)

	assert.NotNil(t, result.Find("plugin-only1"))
	assert.NotNil(t, result.Find("plugin-only2"))
}
