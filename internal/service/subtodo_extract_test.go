package service_test

import (
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/stretchr/testify/assert"
)

func TestExtractSubtodosEmpty(t *testing.T) {
	assert.Nil(t, service.ExtractSubtodosFromDescription(""))
	assert.Nil(t, service.ExtractSubtodosFromDescription("just prose, no boxes"))
}

func TestExtractSubtodosMixedPoseAndBoxes(t *testing.T) {
	desc := "Intro paragraph.\n\n" +
		"- [ ] first thing\n" +
		"- [x] second thing\n" +
		"\nSome closing prose.\n"

	got := service.ExtractSubtodosFromDescription(desc)
	assert.Len(t, got, 2)

	assert.Equal(t, "item-1", got[0].ID)
	assert.Equal(t, "first thing", got[0].Text)
	assert.False(t, got[0].Done)
	assert.False(t, got[0].Required)

	assert.Equal(t, "item-2", got[1].ID)
	assert.Equal(t, "second thing", got[1].Text)
	assert.True(t, got[1].Done)
}

func TestExtractSubtodosSkipsNested(t *testing.T) {
	desc := "- [ ] top level\n" +
		"  - [ ] nested child\n" +
		"\t- [x] tab-indented child\n" +
		"- [ ] second top level\n"

	got := service.ExtractSubtodosFromDescription(desc)
	assert.Len(t, got, 2)
	assert.Equal(t, "top level", got[0].Text)
	assert.Equal(t, "second top level", got[1].Text)
}

func TestExtractSubtodosAcceptsAsteriskAndPlus(t *testing.T) {
	desc := "* [ ] star marker\n+ [ ] plus marker\n"
	got := service.ExtractSubtodosFromDescription(desc)
	assert.Len(t, got, 2)
	assert.Equal(t, "star marker", got[0].Text)
	assert.Equal(t, "plus marker", got[1].Text)
}

func TestExtractSubtodosIgnoresNonCheckboxBullets(t *testing.T) {
	desc := "- plain bullet, not a checkbox\n- [ ] real checkbox\n"
	got := service.ExtractSubtodosFromDescription(desc)
	assert.Len(t, got, 1)
	assert.Equal(t, "real checkbox", got[0].Text)
}

func TestExtractSubtodosUppercaseXIsDone(t *testing.T) {
	desc := "- [X] upper\n"
	got := service.ExtractSubtodosFromDescription(desc)
	assert.Len(t, got, 1)
	assert.True(t, got[0].Done)
}
