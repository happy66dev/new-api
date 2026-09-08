package model

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm"
)

// MaxTransferTargets 限制单次搜索返回的候选人数，避免一次拉太多行浪费资源，也给用户预览留足够空间。
// 主人注意：搜索走 LIKE 模糊查询，数据量大时建议后续给该功能加索引或改用全文检索优化喵。
const MaxTransferTargets = 10

// MinTransferQuota 返回单笔用户间转账的最小内部额度（约等于 0.01 美元）。
// 兜底换算：QuotaPerUnit 是 1 美元的额度数，除以 100 即 1 美分；换算异常时至少为 1，防止出现零/负最小值喵~防御。
func MinTransferQuota() int {
	if common.QuotaPerUnit <= 0 {
		return 1
	}
	return int(common.QuotaPerUnit / 100)
}

// SearchTransferTargets 供普通用户在转账弹窗里按“用户名/显示名/用户ID”实时搜索可接收转账的用户。
// 只返回少量必要字段（id/username/display_name/status/role），不暴露邮箱、注册IP等敏感信息喵。
// 入参：excludeUserId 用于排除发起人自己；keyword 为用户输入的搜索关键词。返回上限 MaxTransferTargets 条。
func SearchTransferTargets(excludeUserId int, keyword string) ([]User, error) {
	// 防御：关键词去除首尾空格后为空，直接报错，避免空关键词把全量用户搜出来泄漏名单喵~防御
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, errors.New("搜索关键词不能为空")
	}

	// 模糊匹配模式：用户名或显示名里包含关键词即命中
	likePattern := "%" + keyword + "%"
	likeCondition := "(username LIKE ? OR display_name LIKE ?)"
	likeArgs := []interface{}{likePattern, likePattern}

	// 若关键词能解析成数字，则把它当作精确的用户ID也加入条件，方便直接输入ID预览目标喵
	if keywordID, err := strconv.Atoi(keyword); err == nil {
		likeCondition = "(id = ? OR username LIKE ? OR display_name LIKE ?)"
		likeArgs = []interface{}{keywordID, likePattern, likePattern}
	}

	// 组装查询：只找启用状态、非软删除、且不是发起人自己的用户
	query := DB.Model(&User{}).
		Where("status = ?", common.UserStatusEnabled).
		Where(likeCondition, likeArgs...)
	if excludeUserId > 0 {
		query = query.Where("id <> ?", excludeUserId)
	}

	// 返回必要字段并限量，防止结果过多拖慢下拉预览
	users := make([]User, 0, MaxTransferTargets)
	err := query.Select("id", "username", "display_name", "status", "role").
		Order("id ASC").
		Limit(MaxTransferTargets).
		Find(&users).Error
	return users, err
}

// TransferQuotaBetweenUsers 在单事务内把转出方主余额 quota 额度原子转入收款方主余额。
// 返回双方用户名供调用方拼写日志文案；出错时事务整体回滚，保证不会出现“扣了没加”或“加了没扣”喵。
// 入参：senderId 转出方用户ID；receiverId 收款方用户ID；quota 要转的内部额度数。
func TransferQuotaBetweenUsers(senderId int, receiverId int, quota int) (senderName string, receiverName string, err error) {
	// 防御：转账额度低于最小值或为负数直接拒绝，避免尘点转账或除零/负扣款喵~防御
	if quota < MinTransferQuota() {
		return "", "", fmt.Errorf("转账额度最小为%s", logger.LogQuota(MinTransferQuota()))
	}
	// 防御：不允许给自己转账，避免无意义的自转日志喵~防御
	if senderId == receiverId {
		return "", "", errors.New("不能转账给自己")
	}
	// 防御：参数非法的用户ID直接拒绝，防止数据库误伤喵~防御
	if senderId <= 0 || receiverId <= 0 {
		return "", "", errors.New("用户参数错误")
	}
	// 防御：单笔转账不能超过钱包上限，防止一次写爆钱包额度（1<<53-1 的 JavaScript 安全上限）喵~防御
	if err := common.ValidateWalletQuota(quota); err != nil {
		return "", "", err
	}

	// 事务内完成“锁行→校验→扣/加”，任何一步失败都整体回滚，保证资金一致喵
	err = DB.Transaction(func(tx *gorm.DB) error {
		// 把两个用户ID按从小到大排序，先锁 id 较小的一方，保证并发转账时加锁顺序一致、避免死锁
		// （lockForUpdate 在 SQLite 下自动跳过，SQLite 单写者模型本身会拒绝并发写冲突喵）
		firstId, secondId := senderId, receiverId
		if firstId > secondId {
			firstId, secondId = secondId, firstId
		}

		// 分别锁定两个用户行，这里先取小ID的那个
		var firstUser, secondUser User
		if err := lockForUpdate(tx).Where("id = ?", firstId).First(&firstUser).Error; err != nil {
			// 防御：找不到目标用户时返回友好错误，不回滚到不明原因喵~防御
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("用户不存在或已被删除")
			}
			return err
		}
		// 再锁定大ID的那个
		if err := lockForUpdate(tx).Where("id = ?", secondId).First(&secondUser).Error; err != nil {
			// 防御：同上，找不到时返回友好错误喵~防御
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("用户不存在或已被删除")
			}
			return err
		}

		// 根据真实ID把两个用户行分别归位到转出方与收款方
		sender := &firstUser
		receiver := &secondUser
		if firstId == receiverId {
			sender, receiver = &secondUser, &firstUser
		}

		// 防御：转出方账号被停用不允许转出，防止异常账号继续操作资产喵~防御
		if sender.Status != common.UserStatusEnabled {
			return errors.New("账号状态异常，无法转出")
		}
		// 防御：收款方账号被停用不允许转入，避免向失效账号打额度喵~防御
		if receiver.Status != common.UserStatusEnabled {
			return errors.New("对方账号状态异常，无法接收转账")
		}
		// 防御：转出方余额不足直接拒绝，用 int64 比较避免加法溢出喵~防御
		if int64(sender.Quota) < int64(quota) {
			return errors.New("余额不足")
		}
		// 防御：收款方加上这笔钱后不能超过钱包上限，超限直接回滚喵~防御
		if int64(receiver.Quota)+int64(quota) > common.MaxWalletQuota {
			return errors.New("对方钱包额度已达上限")
		}

		// 原子扣减转出方额度，where 里再带一次余额守卫，双保险防止并发超扣
		result := tx.Model(&User{}).Where("id = ? AND quota >= ?", sender.Id, quota).
			Update("quota", gorm.Expr("quota - ?", quota))
		if result.Error != nil {
			return result.Error
		}
		// 防御：守卫没命中（余额被并发改掉）则报余额不足并回滚喵~防御
		if result.RowsAffected != 1 {
			return errors.New("余额不足")
		}

		// 原子增加收款方额度
		result = tx.Model(&User{}).Where("id = ?", receiver.Id).
			Update("quota", gorm.Expr("quota + ?", quota))
		if result.Error != nil {
			return result.Error
		}
		// 防御：收款方行未被更新则视为异常，回滚喵~防御
		if result.RowsAffected != 1 {
			return errors.New("转账失败，请稍后重试")
		}

		// 把双方用户名带出事务，供提交后写日志使用（日志库可能独立于主库，不能在事务内写喵）
		senderName = sender.Username
		receiverName = receiver.Username
		return nil
	})
	// 事务失败时，说明双方余额都未改动，直接向上抛错，不写任何日志喵
	if err != nil {
		return "", "", err
	}

	// 事务提交成功后，用 goroutine 异步同步双方额度缓存，保证 Redis 缓存里余额与库里一致；
	// 缓存不存在时这些函数内部会自动跳过，下次读取会从数据库重新水合，所以即使失败也不影响正确性喵
	gopool.Go(func() {
		if err := cacheDecrUserQuota(senderId, int64(quota)); err != nil {
			common.SysLog("failed to sync transfer-out to user quota cache: " + err.Error())
		}
	})
	gopool.Go(func() {
		syncCreditUserQuotaCache(receiverId, quota, "transfer")
	})

	// 给转出方记一条“转出”日志，内容写明收款方与金额，方便转出方与管理员查看明细
	RecordLog(senderId, LogTypeTransfer,
		fmt.Sprintf("向用户 %s(ID:%d) 转账 %s", receiverName, receiverId, logger.LogQuota(quota)))
	// 给收款方记一条“收到转账”日志，内容写明转出方与金额，方便收款方与管理员查看明细
	RecordLog(receiverId, LogTypeTransfer,
		fmt.Sprintf("收到来自用户 %s(ID:%d) 的转账 %s", senderName, senderId, logger.LogQuota(quota)))
	return senderName, receiverName, nil
}
