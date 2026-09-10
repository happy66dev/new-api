package model

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// 用户上游模型功能开关的系统设置键名喵。
// 开关1 UserUpstreamEnabled：总开关，控制"用户是否允许添加并自用自建上游(user/xxx)"喵。
// 开关2 UserUpstreamSharingEnabled：共享开关，控制"用户是否允许把自己上游共享给其他用户(user-shared 分组)"喵。
const (
	// UserUpstreamEnabledKey 系统设置键：允许用户添加/自用自建上游；关闭即完全冻结该功能喵。
	UserUpstreamEnabledKey = "UserUpstreamEnabled"
	// UserUpstreamSharingEnabledKey 系统设置键：允许用户共享自建上游；关闭即停共享并隐藏 user-shared 分组喵。
	UserUpstreamSharingEnabledKey = "UserUpstreamSharingEnabled"
)

// UserUpstreamFeatureEnabled 读取总开关当前状态，默认开启（与 model/option.go 初始化一致）喵。
// 关闭后数据面/控制面都会拒绝 user/xxx 相关的调用与新增，达到"完全冻结、数据存档"语义喵。
func UserUpstreamFeatureEnabled() bool {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	// 缺省（未配置或空串）视为开启，与 InitOptionMap 默认一致；只有显式 "false" 才是关闭喵。
	return common.OptionMap[UserUpstreamEnabledKey] != "false"
}

// UserUpstreamSharingFeatureEnabled 读取共享开关当前状态，默认开启喵。
// 关闭后 user-shared 分组不再追加到可用分组，且写接口拒绝把 share_enabled 置为 true 喵。
func UserUpstreamSharingFeatureEnabled() bool {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	// 缺省（未配置或空串）视为开启，与 InitOptionMap 默认一致；只有显式 "false" 才是关闭喵。
	return common.OptionMap[UserUpstreamSharingEnabledKey] != "false"
}

// SetUserUpstreamFeatureOption 事务性地写入用户上游模型功能开关，并在"开启→关闭"跃迁时执行全站清理喵。
// 清理要点（对应用户确认的产品语义）：
//   - 总开关关闭：不删除任何 user_upstream_models 数据行（仅存档），但删除全站所有"虚拟模型自定义候选引用用户上游"的引用，
//     并联动把共享开关写为 false（强制第二个开关也关闭）喵。
//   - 共享开关关闭：批量把全站 share_enabled 置 false（强制停共享），删除全站"虚拟模型引用正被共享的上游"的自定义候选喵。
//   - 候选引用一旦删除不可恢复；重新打开开关时用户需手动重建虚拟候选或重新开启共享喵。
//
// 为什么放在 model 层：/api/option 的 PUT 以及 UpdateOptionsBulk 都汇聚到 model.UpdateOption/updateOptionMap，
// 在此单点拦截可保证所有写入口行为一致，且不污染每次启动同步 options 的 updateOptionMap 热路径喵。
func SetUserUpstreamFeatureOption(key string, value string) error {
	// 喵~防御：只接受合法的布尔字符串，避免把垃圾值误判为关闭触发大清理喵。
	if value != "true" && value != "false" {
		return errors.New("用户上游模型功能开关取值必须为 true 或 false")
	}
	// 读取旧值（尚未加锁写），作为"是否发生关闭跃迁"的判断基准喵。
	common.OptionMapRWMutex.RLock()
	oldMasterOn := common.OptionMap[UserUpstreamEnabledKey] == "true"
	oldSharingOn := common.OptionMap[UserUpstreamSharingEnabledKey] == "true"
	common.OptionMapRWMutex.RUnlock()

	// 由本次写入的键推导两把开关的最终状态喵。
	newMasterOn := oldMasterOn
	newSharingOn := oldSharingOn
	switch key {
	case UserUpstreamEnabledKey:
		// 总开关由管理员显式设置喵。
		newMasterOn = value == "true"
		// 总开关关闭时强制联动关闭共享开关，满足"关闭则下面的也关闭"喵。
		if !newMasterOn {
			newSharingOn = false
		}
	case UserUpstreamSharingEnabledKey:
		// 共享开关由管理员显式设置喵。
		newSharingOn = value == "true"
		// 喵~防御：总开关关闭时不允许单独把共享打开，避免绕过总功能冻结喵。
		if newSharingOn && !newMasterOn {
			return errors.New("总开关关闭时不能开启共享，请先启用用户上游模型")
		}
	default:
		return fmt.Errorf("未知的用户上游模型功能开关键: %s", key)
	}

	// 判断本操作是否触发"某功能从开转为关"，只有真正的关闭跃迁才需要执行数据清理喵。
	masterDisabling := oldMasterOn && !newMasterOn
	sharingDisabling := oldSharingOn && !newSharingOn

	// 选项写入与数据清理放在同一个事务里，保证任何一步失败都不会留下"开关已关但引用没清"的中间态喵。
	err := DB.Transaction(func(tx *gorm.DB) error {
		// 先持久化两把开关行，保证即便清理中途进程重启，数据库里的开关状态仍是关闭喵。
		if err := upsertOptionRow(tx, UserUpstreamEnabledKey, strconv.FormatBool(newMasterOn)); err != nil {
			return err
		}
		if err := upsertOptionRow(tx, UserUpstreamSharingEnabledKey, strconv.FormatBool(newSharingOn)); err != nil {
			return err
		}
		switch {
		case masterDisabling:
			// 总开关关闭：清掉全站所有"虚拟模型引用用户上游"的候选引用（上游数据行保留存档）喵。
			if err := purgeVirtualModelUpstreamReferences(tx, nil); err != nil {
				return err
			}
		case sharingDisabling:
			// 共享开关关闭（总开关仍开）：只清引用"当前正被共享"上游的候选引用喵。
			sharedIDs, collectErr := collectSharingUserUpstreamIDs(tx)
			if collectErr != nil {
				return collectErr
			}
			if err := purgeVirtualModelUpstreamReferences(tx, sharedIDs); err != nil {
				return err
			}
		}
		// 共享被关（单独关或随总开关联动关）时，全站批量撤销 share_enabled 标志，立即停共享喵。
		if masterDisabling || sharingDisabling {
			if err := revokeAllUserUpstreamSharing(tx); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	// 数据库提交成功后刷新内存 OptionMap，让运行时门禁立即读到最新开关状态喵。
	common.OptionMapRWMutex.Lock()
	common.OptionMap[UserUpstreamEnabledKey] = strconv.FormatBool(newMasterOn)
	common.OptionMap[UserUpstreamSharingEnabledKey] = strconv.FormatBool(newSharingOn)
	common.OptionMapRWMutex.Unlock()
	return nil
}

// upsertOptionRow 使用给定连接写入单个选项行（不存在则新建，存在则覆盖值）喵。
func upsertOptionRow(database *gorm.DB, key string, value string) error {
	// 喵~防御：空连接直接拒绝，避免在事务外裸跑破坏一致性喵。
	if database == nil {
		return errors.New("数据库不可用")
	}
	option := Option{Key: key}
	// 先按主键 key 读取，不存在时插入一条空记录喵。
	if err := database.FirstOrCreate(&option, Option{Key: key}).Error; err != nil {
		return err
	}
	option.Value = value
	// Save 同时覆盖创建/更新全部字段喵。
	return database.Save(&option).Error
}

// collectSharingUserUpstreamIDs 返回当前 share_enabled=true 的全部上游条目 id 集合喵。
// 必须在撤销共享标志之前调用，否则命中集会变成空喵。
func collectSharingUserUpstreamIDs(database *gorm.DB) ([]int64, error) {
	var sharedIDs []int64
	// 只挑共享标志为真的行，余额/额度是否耗尽不影响"引用归属"判定喵。
	if err := database.Model(&UserUpstreamModel{}).Where("share_enabled = ?", true).Pluck("id", &sharedIDs).Error; err != nil {
		return nil, err
	}
	return sharedIDs, nil
}

// revokeAllUserUpstreamSharing 批量撤销全站上游条目的共享标志，实现"关闭共享即全站停共享"喵。
func revokeAllUserUpstreamSharing(database *gorm.DB) error {
	// 喵~防御：空连接直接拒绝，避免在事务外裸跑破坏一致性喵。
	if database == nil {
		return errors.New("数据库不可用")
	}
	// 只更新仍处于共享状态的行，零行更新也属正常（幂等）喵。
	return database.Model(&UserUpstreamModel{}).Where("share_enabled = ?", true).Update("share_enabled", false).Error
}

// purgeVirtualModelUpstreamReferences 删除虚拟模型里"引用用户上游"的自定义候选及其关联数据喵。
// upstreamModelIDs 为 nil 时表示清除全部引用（总开关关闭场景）；否则只清除引用这些指定上游条目的候选（共享开关关闭场景）喵。
// 删除粒度 = 只删引用候选本身（候选行 + 加密配置 + 规则 + 冻结），保留虚拟模型及其余 internal/直填候选喵。
func purgeVirtualModelUpstreamReferences(database *gorm.DB, upstreamModelIDs []int64) error {
	// 喵~防御：空连接直接拒绝，避免在事务外裸跑破坏一致性喵。
	if database == nil {
		return errors.New("数据库不可用")
	}
	// 先按引用条件收集要删除的候选编号（candidate_id）喵。
	var candidateIDs []int
	query := database.Model(&VirtualModelCustomCandidate{}).Where("upstream_model_id IS NOT NULL")
	if upstreamModelIDs != nil {
		// 喵~防御：指定集合为空说明当前没有可清理对象，直接返回避免空条件生成全表删除喵。
		if len(upstreamModelIDs) == 0 {
			return nil
		}
		query = database.Model(&VirtualModelCustomCandidate{}).Where("upstream_model_id IN ?", upstreamModelIDs)
	}
	if err := query.Pluck("candidate_id", &candidateIDs).Error; err != nil {
		return err
	}
	// 喵~防御：没有命中任何候选则无事可做，提前返回保持幂等喵。
	if len(candidateIDs) == 0 {
		return nil
	}
	// 事务内级联删除候选及其规则、冻结和加密配置，保证一致性（与孤儿候选清理模式一致）喵。
	if err := database.Unscoped().Where("candidate_id IN ?", candidateIDs).Delete(&VirtualModelFailureRule{}).Error; err != nil {
		return err
	}
	if err := database.Unscoped().Where("candidate_id IN ?", candidateIDs).Delete(&VirtualModelCustomCandidate{}).Error; err != nil {
		return err
	}
	if err := database.Unscoped().Where("candidate_id IN ?", candidateIDs).Delete(&VirtualModelManualFreeze{}).Error; err != nil {
		return err
	}
	// 喵~防御：候选的自动冻结状态按 owner+candidate 键控，清理该候选时一并硬删避免孤儿冻结喵。
	if err := database.Unscoped().Where("candidate_id IN ?", candidateIDs).Delete(&VirtualModelInternalFreezeState{}).Error; err != nil {
		return err
	}
	// 喵~防御：候选行使用硬删除，避免软删除残留占位并污染候选排序喵。
	if err := database.Unscoped().Where("id IN ?", candidateIDs).Delete(&VirtualModelCandidate{}).Error; err != nil {
		return err
	}
	// 清理该候选的节点探测状态行（scope=virtual_candidate, entity=候选），避免孤儿状态残留喵。
	if err := database.Where("scope = ? AND entity_id IN ?", EntityProbeScopeVirtualCandidate, candidateIDs).Delete(&EntityProbeState{}).Error; err != nil {
		return err
	}
	common.SysLog(fmt.Sprintf("user upstream feature disabled: purged %d virtual model upstream candidate references", len(candidateIDs)))
	return nil
}
