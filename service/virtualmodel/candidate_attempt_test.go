package virtualmodel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatCandidateAttemptIDProducesShortUniqueIdentifiers(t *testing.T) {
	// 同一个候选的不同尝试序号必须得到不同标识喵。
	firstAttemptID, firstError := FormatCandidateAttemptID(42, 1)
	require.NoError(t, firstError)
	assert.Equal(t, "vc42a1", firstAttemptID)

	secondAttemptID, secondError := FormatCandidateAttemptID(42, 2)
	require.NoError(t, secondError)
	assert.NotEqual(t, firstAttemptID, secondAttemptID)

	// 不同候选即使处于同一尝试序号也必须得到不同标识喵。
	otherCandidateAttemptID, otherError := FormatCandidateAttemptID(43, 1)
	require.NoError(t, otherError)
	assert.NotEqual(t, firstAttemptID, otherCandidateAttemptID)

	// 上限组合仍需保持在可拼进 request_id 列宽的长度内喵。
	maximumAttemptID, maximumError := FormatCandidateAttemptID(maximumCandidateIDForAttemptID, maximumAttemptSequence)
	require.NoError(t, maximumError)
	assert.LessOrEqual(t, len(maximumAttemptID), 32)
}

func TestFormatCandidateAttemptIDRejectsOutOfRangeInput(t *testing.T) {
	// 喵~防御：候选编号为零或负数说明没有真正激活候选喵。
	_, zeroCandidateError := FormatCandidateAttemptID(0, 1)
	require.Error(t, zeroCandidateError)

	_, negativeCandidateError := FormatCandidateAttemptID(-7, 1)
	require.Error(t, negativeCandidateError)

	// 喵~防御：候选编号超上限时标识会变长并可能挤爆幂等键预算喵。
	_, hugeCandidateError := FormatCandidateAttemptID(maximumCandidateIDForAttemptID+1, 1)
	require.Error(t, hugeCandidateError)

	// 喵~防御：尝试序号必须从 1 开始，零值会让两次尝试共享同一个标识喵。
	_, zeroSequenceError := FormatCandidateAttemptID(42, 0)
	require.Error(t, zeroSequenceError)

	_, negativeSequenceError := FormatCandidateAttemptID(42, -1)
	require.Error(t, negativeSequenceError)

	// 喵~防御：尝试序号触顶说明候选循环失控，必须显式失败喵。
	_, hugeSequenceError := FormatCandidateAttemptID(42, maximumAttemptSequence+1)
	require.Error(t, hugeSequenceError)
}

func TestFormatCandidateAttemptIDUsesOnlySafeCharacters(t *testing.T) {
	attemptID, attemptError := FormatCandidateAttemptID(987654321, 4321)
	require.NoError(t, attemptError)
	for _, character := range attemptID {
		// 判断当前字符是否为小写字母喵。
		isLowerCaseLetter := character >= 'a' && character <= 'z'
		// 判断当前字符是否为数字喵。
		isDigit := character >= '0' && character <= '9'
		// 标识只能出现小写字母与数字，禁止冒号、斜杠、空格等会破坏日志解析的字符喵。
		assert.True(t, isLowerCaseLetter || isDigit, "attempt id %q contains unsafe character %q", attemptID, character)
	}
}
