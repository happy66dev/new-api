package service

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// EnsureReliefPlan 启动时确保救济金隐藏套餐存在，并把其 id 回写进 relief_setting.plan_id 喵~
//
// 整体思路（主人拍板"启动自动建套餐并回写"）：
//  1. 只在主节点执行：建套餐 / 写配置都要落库，多实例下只让 master 做，避免并发重复创建；
//  2. 若 relief_setting.plan_id 已指向一个仍存在的套餐，则无需任何操作；
//  3. 否则先按标题复用已有的隐藏套餐（例如之前建过但配置被重置），复用不到再新建；
//  4. 最后把套餐 id 通过 UpdateOption 回写（既持久化到 options 表，又热更新内存里的配置结构体）。
//
// 本函数不返回错误：启动流程不应因它失败而中断；失败时记录系统日志，领取接口已有 plan_id<=0 的兜底提示。
func EnsureReliefPlan() {
	// 只在主节点建套餐/写配置，从节点直接跳过（避免并发重复创建与写冲突）
	if !common.IsMasterNode {
		return
	}

	// 读取救济金配置（指针，仅读）
	setting := operation_setting.GetReliefSetting()

	// 已配置 plan_id：若其指向的套餐仍存在，说明一切就绪，直接返回
	if setting.PlanId > 0 {
		if _, err := model.GetSubscriptionPlanById(setting.PlanId); err == nil {
			return
		}
		// 走到这里说明 plan_id 指向的套餐已被删除，继续往下查找/新建并回写
	}

	// 先尝试按标题复用已有的隐藏套餐，避免重复创建
	plan, err := model.FindReliefPlan()
	if err != nil {
		// 喵~防御：查找失败就记日志放弃本次，不中断启动
		common.SysError("查找救济金套餐失败: " + err.Error())
		return
	}
	// 没有现成的就按配置的默认发放额度新建一张
	if plan == nil {
		plan, err = model.CreateReliefPlan(setting.GrantAmountUsd)
		if err != nil {
			// 喵~防御：创建失败记日志放弃本次，不中断启动
			common.SysError("创建救济金套餐失败: " + err.Error())
			return
		}
		common.SysLog("已自动创建救济金隐藏套餐，id=" + strconv.Itoa(plan.Id))
	}

	// 把套餐 id 回写到 relief_setting.plan_id（持久化 + 热更新内存配置）
	if err := model.UpdateOption("relief_setting.plan_id", strconv.Itoa(plan.Id)); err != nil {
		// 喵~防御：回写失败记日志；下次启动会再次尝试，不影响其它功能
		common.SysError("回写救济金套餐 id 失败: " + err.Error())
		return
	}
}
