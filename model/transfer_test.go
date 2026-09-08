package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTransferTestUser 造一个唯一的测试用户：余额 quota、状态 status，用户名/邀请码都用随机串避免撞唯一索引喵。
func createTransferTestUser(t *testing.T, username string, quota int, status int) User {
	t.Helper()
	suffix := common.GetRandomString(8)
	user := User{
		Username: username + "-" + suffix,
		Password: "unused-password-hash",
		Role:     common.RoleCommonUser,
		Status:   status,
		Group:    "default",
		Quota:    quota,
		AffCode:  "transfer-aff-" + suffix,
	}
	require.NoError(t, DB.Create(&user).Error)
	return user
}

// getUserQuotaOfTransferTarget 从数据库读某用户当前主余额，用于断言转账后的余额变化喵。
func getUserQuotaOfTransferTarget(t *testing.T, id int) int {
	t.Helper()
	var user User
	require.NoError(t, DB.Select("quota").First(&user, id).Error)
	return user.Quota
}

// countTransferLogs 数某个用户在 logs 表里的“用户间转账”日志条数，用于断言双方各有明细喵。
func countTransferLogs(t *testing.T, userId int) int {
	t.Helper()
	var total int64
	require.NoError(t, DB.Model(&Log{}).Where("user_id = ? AND type = ?", userId, LogTypeTransfer).Count(&total).Error)
	return int(total)
}

func TestTransferQuotaBetweenUsersHappyPath(t *testing.T) {
	truncateTables(t)
	// 转出方余额 200000 额度，收款方初始 0，转出 50000 额度喵
	sender := createTransferTestUser(t, "transfer-sender", 200000, common.UserStatusEnabled)
	receiver := createTransferTestUser(t, "transfer-receiver", 0, common.UserStatusEnabled)

	// 调用待测事务函数，应成功并返回双方用户名喵
	senderName, receiverName, err := TransferQuotaBetweenUsers(sender.Id, receiver.Id, 50000)
	require.NoError(t, err)
	assert.Equal(t, sender.Username, senderName)
	assert.Equal(t, receiver.Username, receiverName)

	// 断言余额：转出方减 50000，收款方加 50000喵
	assert.Equal(t, 150000, getUserQuotaOfTransferTarget(t, sender.Id))
	assert.Equal(t, 50000, getUserQuotaOfTransferTarget(t, receiver.Id))

	// 断言日志：转出方与收款方各有一条转账日志，明细落库喵
	assert.Equal(t, 1, countTransferLogs(t, sender.Id))
	assert.Equal(t, 1, countTransferLogs(t, receiver.Id))
}

func TestTransferQuotaBetweenUsersInsufficientBalance(t *testing.T) {
	truncateTables(t)
	// 转出方余额只有 1000，低于待转的 50000，转账必须失败喵
	sender := createTransferTestUser(t, "transfer-poor", 1000, common.UserStatusEnabled)
	receiver := createTransferTestUser(t, "transfer-rich", 50000, common.UserStatusEnabled)

	// 预期返回余额不足的错误喵
	_, _, err := TransferQuotaBetweenUsers(sender.Id, receiver.Id, 50000)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "余额不足")

	// 断言双方余额都保持不变，事务整体回滚喵
	assert.Equal(t, 1000, getUserQuotaOfTransferTarget(t, sender.Id))
	assert.Equal(t, 50000, getUserQuotaOfTransferTarget(t, receiver.Id))
	// 失败时不应产生任何转账日志喵
	assert.Equal(t, 0, countTransferLogs(t, sender.Id))
	assert.Equal(t, 0, countTransferLogs(t, receiver.Id))
}

func TestTransferQuotaBetweenUsersSelfTransferRejected(t *testing.T) {
	truncateTables(t)
	// 同一个用户不能给自己转账，应直接报错（金额取最小额度，先绕开最小额度分支喵）
	user := createTransferTestUser(t, "transfer-self", 100000, common.UserStatusEnabled)
	_, _, err := TransferQuotaBetweenUsers(user.Id, user.Id, MinTransferQuota())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "不能转账给自己")
}

func TestTransferQuotaBetweenUsersBelowMinimumRejected(t *testing.T) {
	truncateTables(t)
	// 转账额度低于最小额度时应报错，且余额不动喵
	sender := createTransferTestUser(t, "transfer-min-s", 100000, common.UserStatusEnabled)
	receiver := createTransferTestUser(t, "transfer-min-r", 0, common.UserStatusEnabled)
	_, _, err := TransferQuotaBetweenUsers(sender.Id, receiver.Id, MinTransferQuota()-1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "最小")
	assert.Equal(t, 100000, getUserQuotaOfTransferTarget(t, sender.Id))
	assert.Equal(t, 0, getUserQuotaOfTransferTarget(t, receiver.Id))
}

func TestTransferQuotaBetweenUsersRejectsDisabledReceiver(t *testing.T) {
	truncateTables(t)
	// 收款方被停用时转账必须被拒绝，防止向失效账号打额度喵
	sender := createTransferTestUser(t, "transfer-dis-s", 100000, common.UserStatusEnabled)
	receiver := createTransferTestUser(t, "transfer-dis-r", 0, common.UserStatusDisabled)
	_, _, err := TransferQuotaBetweenUsers(sender.Id, receiver.Id, MinTransferQuota())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "状态异常")
	assert.Equal(t, 100000, getUserQuotaOfTransferTarget(t, sender.Id))
	assert.Equal(t, 0, getUserQuotaOfTransferTarget(t, receiver.Id))
}

func TestTransferQuotaBetweenUsersRejectsNonexistentReceiver(t *testing.T) {
	truncateTables(t)
	// 收款方不存在时返回“用户不存在”友好错误喵
	sender := createTransferTestUser(t, "transfer-ghost-s", 100000, common.UserStatusEnabled)
	_, _, err := TransferQuotaBetweenUsers(sender.Id, 99999999, MinTransferQuota())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "用户不存在")
}

func TestSearchTransferTargets(t *testing.T) {
	truncateTables(t)
	// 造三个用户：alice（启用）、bob（启用）、carol（停用），便于验证过滤规则喵
	alice := createTransferTestUser(t, "alice", 100, common.UserStatusEnabled)
	bob := createTransferTestUser(t, "bob", 100, common.UserStatusEnabled)
	carol := createTransferTestUser(t, "carol", 100, common.UserStatusDisabled)
	// 断言测试夹具本身生效：carol 确实处于停用状态喵
	assert.Equal(t, common.UserStatusDisabled, carol.Status)

	// 用 alice 的前缀搜索，应命中启用状态的 alice（自身被排除规则另测）喵
	found, err := SearchTransferTargets(bob.Id, "alice")
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, alice.Id, found[0].Id)
	assert.Equal(t, alice.Username, found[0].Username)

	// 停用用户 carol 不应出现在结果里喵
	foundDisabled, err := SearchTransferTargets(bob.Id, "carol")
	require.NoError(t, err)
	assert.Empty(t, foundDisabled)

	// 用 alice 的精确数字ID搜索也应命中，支持“直接输入ID预览”喵
	foundByID, err := SearchTransferTargets(bob.Id, fmt.Sprintf("%d", alice.Id))
	require.NoError(t, err)
	require.Len(t, foundByID, 1)
	assert.Equal(t, alice.Id, foundByID[0].Id)

	// 排除自身：alice 搜 “alice” 应搜不到自己喵
	foundSelf, err := SearchTransferTargets(alice.Id, "alice")
	require.NoError(t, err)
	assert.Empty(t, foundSelf)

	// 空关键词直接报错，禁止空搜索枚举全量用户喵
	_, err = SearchTransferTargets(bob.Id, "   ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "关键词")
}
