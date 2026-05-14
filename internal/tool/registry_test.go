package tool_test

import (
	"testing"

	"github.com/hollis-labs/torque/internal/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeSimpleTool(name, category string, tags ...string) tool.Tool {
	return tool.NewTool(name, "desc",
		tool.WithCategory(category),
		tool.WithTags(tags...),
	)
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := tool.NewRegistry()
	tl := makeSimpleTool("my-tool", tool.CategoryCoreIO)
	r.Register(tl)

	got := r.Get("my-tool")
	require.NotNil(t, got)
	assert.Equal(t, "my-tool", got.Name())
}

func TestRegistry_GetMissing(t *testing.T) {
	r := tool.NewRegistry()
	assert.Nil(t, r.Get("nonexistent"))
}

func TestRegistry_RegisterAll(t *testing.T) {
	r := tool.NewRegistry()
	tools := []tool.Tool{
		makeSimpleTool("t1", tool.CategoryCoreIO),
		makeSimpleTool("t2", tool.CategorySearch),
		makeSimpleTool("t3", tool.CategoryMCP),
	}
	r.RegisterAll(tools)
	assert.Equal(t, 3, r.Count())
}

func TestRegistry_Replacement(t *testing.T) {
	r := tool.NewRegistry()
	r.Register(makeSimpleTool("tool-a", tool.CategoryCoreIO))
	r.Register(makeSimpleTool("tool-b", tool.CategorySearch))

	// Replace tool-a with a new version in a different category.
	r.Register(makeSimpleTool("tool-a", tool.CategoryMCP))

	// Count should remain 2.
	assert.Equal(t, 2, r.Count())

	got := r.Get("tool-a")
	require.NotNil(t, got)
	assert.Equal(t, tool.CategoryMCP, got.Category())
}

func TestRegistry_AllInsertionOrder(t *testing.T) {
	r := tool.NewRegistry()
	names := []string{"alpha", "beta", "gamma", "delta"}
	for _, n := range names {
		r.Register(makeSimpleTool(n, tool.CategoryCoreIO))
	}

	all := r.All()
	require.Len(t, all, 4)
	for i, n := range names {
		assert.Equal(t, n, all[i].Name())
	}
}

func TestRegistry_AllInsertionOrderAfterReplacement(t *testing.T) {
	r := tool.NewRegistry()
	r.Register(makeSimpleTool("first", tool.CategoryCoreIO))
	r.Register(makeSimpleTool("second", tool.CategorySearch))
	r.Register(makeSimpleTool("third", tool.CategoryMCP))

	// Replace "second" — should stay in slot 2.
	r.Register(makeSimpleTool("second", tool.CategoryAgent))

	all := r.All()
	require.Len(t, all, 3)
	assert.Equal(t, "first", all[0].Name())
	assert.Equal(t, "second", all[1].Name())
	assert.Equal(t, "third", all[2].Name())
	assert.Equal(t, tool.CategoryAgent, all[1].Category())
}

func TestRegistry_GetByNames(t *testing.T) {
	r := tool.NewRegistry()
	r.RegisterAll([]tool.Tool{
		makeSimpleTool("a", tool.CategoryCoreIO),
		makeSimpleTool("b", tool.CategorySearch),
		makeSimpleTool("c", tool.CategoryMCP),
	})

	got := r.GetByNames([]string{"c", "a", "missing"})
	require.Len(t, got, 2)
	assert.Equal(t, "c", got[0].Name())
	assert.Equal(t, "a", got[1].Name())
}

func TestRegistry_ByCategory(t *testing.T) {
	r := tool.NewRegistry()
	r.RegisterAll([]tool.Tool{
		makeSimpleTool("read1", tool.CategoryCoreIO),
		makeSimpleTool("search1", tool.CategorySearch),
		makeSimpleTool("read2", tool.CategoryCoreIO),
		makeSimpleTool("mcp1", tool.CategoryMCP),
	})

	coreIO := r.ByCategory(tool.CategoryCoreIO)
	require.Len(t, coreIO, 2)
	assert.Equal(t, "read1", coreIO[0].Name())
	assert.Equal(t, "read2", coreIO[1].Name())

	search := r.ByCategory(tool.CategorySearch)
	require.Len(t, search, 1)
	assert.Equal(t, "search1", search[0].Name())
}

func TestRegistry_ByTag(t *testing.T) {
	r := tool.NewRegistry()
	r.RegisterAll([]tool.Tool{
		makeSimpleTool("t1", tool.CategoryCoreIO, "fast", "io"),
		makeSimpleTool("t2", tool.CategorySearch, "fast"),
		makeSimpleTool("t3", tool.CategoryMCP, "io"),
		makeSimpleTool("t4", tool.CategoryAgent),
	})

	fast := r.ByTag("fast")
	require.Len(t, fast, 2)
	assert.Equal(t, "t1", fast[0].Name())
	assert.Equal(t, "t2", fast[1].Name())

	io := r.ByTag("io")
	require.Len(t, io, 2)
	assert.Equal(t, "t1", io[0].Name())
	assert.Equal(t, "t3", io[1].Name())
}

func TestRegistry_CountEmpty(t *testing.T) {
	r := tool.NewRegistry()
	assert.Equal(t, 0, r.Count())
}
