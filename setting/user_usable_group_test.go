package setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroupDescriptionsAllowMultilineAndNonSelectableGroups(t *testing.T) {
	originalUsable := UserUsableGroups2JSONString()
	originalDescriptions := GroupDescriptions2JSONString()
	t.Cleanup(func() {
		_ = UpdateUserUsableGroupsByJSONString(originalUsable)
		_ = UpdateGroupDescriptionsByJSONString(originalDescriptions)
	})

	require.NoError(t, UpdateUserUsableGroupsByJSONString(`{"selectable":"selectable group"}`))
	require.NoError(t, UpdateGroupDescriptionsByJSONString(`{"selectable":"line one\nline two","hidden":"visible to the model square\nsecond line"}`))

	assert.Equal(t, "line one\nline two", GetUsableGroupDescription("selectable"))
	assert.Equal(t, "visible to the model square\nsecond line", GetUsableGroupDescription("hidden"))
	_, selectable := GetUserUsableGroupsCopy()["hidden"]
	assert.False(t, selectable)
}
