package controller

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// 虚拟模型候选被动变化的稳定原因码喵。
// 喵~防御：这些是前后端契约，前端按它们查 i18n 文案，因此不得随意改名喵。
const (
	// virtualModelAnomalyGroupNotConfigured 表示内部候选没有配置分组，永远无法执行喵。
	virtualModelAnomalyGroupNotConfigured = "group_not_configured"
	// virtualModelAnomalyAutoGroupNotSupported 表示内部候选用了 auto 分组，虚拟模型不支持自动分组喵。
	virtualModelAnomalyAutoGroupNotSupported = "auto_group_not_supported"
	// virtualModelAnomalyGroupNotAccessible 表示用户分组降级/共享停止后，该候选分组已不可访问喵。
	virtualModelAnomalyGroupNotAccessible = "group_not_accessible"
	// virtualModelAnomalyModelNotAvailable 表示该分组下当前没有这个模型的可用渠道（节点模型被删除/共享停止）喵。
	virtualModelAnomalyModelNotAvailable = "model_not_available"
	// virtualModelAnomalyUpstreamDisabled 表示系统总开关(UserUpstreamEnabled)关闭，自定义候选（直填或引用）一律不可用喵。
	virtualModelAnomalyUpstreamDisabled = "upstream_feature_disabled"
	// virtualModelAnomalyReferencedUpstreamMissing 表示自定义候选引用的用户上游模型已被删除喵。
	virtualModelAnomalyReferencedUpstreamMissing = "referenced_upstream_missing"
)

// virtualModelCandidateAnomaly 描述一个因被动变化而不可用的虚拟模型候选喵。
type virtualModelCandidateAnomaly struct {
	// VirtualModelID 与 VirtualModelName 定位所属虚拟模型，供前端归组显示喵。
	VirtualModelID   int    `json:"virtual_model_id"`
	VirtualModelName string `json:"virtual_model_name"`
	// CandidateID 定位候选，供前端逐候选展示红点喵。
	CandidateID int    `json:"candidate_id"`
	SourceType  string `json:"source_type"`
	// GroupName 与 RealModelName 是该候选的路由目标，供前端定位具体异常项喵。
	GroupName     string `json:"group_name,omitempty"`
	RealModelName string `json:"real_model_name,omitempty"`
	// ReasonCode 是稳定机器可读原因码，前端据此选择 i18n 文案喵。
	ReasonCode string `json:"reason_code"`
	// ReasonMessage 是中文兜底说明，当前端缺少对应翻译时直接展示喵。
	ReasonMessage string `json:"reason_message"`
}

// virtualModelAnomalyReasonMessage 把原因码翻译成中文兜底说明喵。
func virtualModelAnomalyReasonMessage(reasonCode string, candidate model.VirtualModelCandidateSnapshot) string {
	switch reasonCode {
	case virtualModelAnomalyGroupNotConfigured:
		return "内部候选没有配置分组，无法执行"
	case virtualModelAnomalyAutoGroupNotSupported:
		return "虚拟模型候选不支持自动分组"
	case virtualModelAnomalyGroupNotAccessible:
		return "当前账号已无法访问分组「" + candidate.GroupName + "」"
	case virtualModelAnomalyModelNotAvailable:
		return "分组「" + candidate.GroupName + "」下已没有模型「" + candidate.RealModelName + "」的可用渠道"
	case virtualModelAnomalyUpstreamDisabled:
		return "系统已关闭用户自定上游功能"
	case virtualModelAnomalyReferencedUpstreamMissing:
		return "引用的用户上游模型已不存在"
	}
	return "候选当前不可用"
}

// scanVirtualModelCandidateAnomalies 读取时扫描当前用户所有虚拟模型的启用候选，
// 判定因被动变化而不可用的候选，返回异常清单喵。
//
// 判定语义与运行时 middleware/distributor.go 的候选跳过逻辑保持同源：
// 内部候选看「分组是否可访问 + 分组下模型是否存在」，自定义候选看「上游开关 + 引用上游是否还在」喵。
func scanVirtualModelCandidateAnomalies(ownerUserID int, userGroup string) ([]virtualModelCandidateAnomaly, error) {
	// 喵~防御：无效用户不扫描，避免越权读取他人数据喵。
	if ownerUserID <= 0 {
		return []virtualModelCandidateAnomaly{}, nil
	}
	// 加载当前用户全部虚拟模型，与列表接口同口径喵。
	var virtualModels []model.VirtualModel
	if err := model.DB.Where("owner_user_id = ?", ownerUserID).Order("id desc").Find(&virtualModels).Error; err != nil {
		return nil, err
	}
	// 可用分组只计算一次，供所有内部候选共享判定喵。
	usableGroups := service.GetUserUsableGroupsForUser(ownerUserID, userGroup)
	// 主人注意：不同分组的启模型列表只查一次并缓存，把 N 个候选的 N 次查询压到「不同分组数」次；
	// 分组数上限就是候选数量上限 32，且只有异常判定需要，不会拖慢正常调用喵。
	enabledModelsByGroup := make(map[string]map[string]bool)
	anomalies := make([]virtualModelCandidateAnomaly, 0)
	for modelIndex := range virtualModels {
		virtualModel := &virtualModels[modelIndex]
		// 只扫描启用候选，与运行时加载口径一致（用户主动禁用的候选无需提醒）喵。
		candidateSnapshots, candidateError := model.GetEnabledVirtualModelCandidateSnapshotsWithDB(model.DB, virtualModel.ID)
		if candidateError != nil {
			return nil, candidateError
		}
		for candidateIndex := range candidateSnapshots {
			candidateSnapshot := &candidateSnapshots[candidateIndex]
			reasonCode := judgeVirtualModelCandidateAnomaly(candidateSnapshot, ownerUserID, usableGroups, enabledModelsByGroup)
			if reasonCode == "" {
				continue
			}
			anomalies = append(anomalies, virtualModelCandidateAnomaly{
				VirtualModelID:   virtualModel.ID,
				VirtualModelName: virtualModel.NormalizedName,
				CandidateID:      candidateSnapshot.CandidateID,
				SourceType:       string(candidateSnapshot.SourceType),
				GroupName:        candidateSnapshot.GroupName,
				RealModelName:    candidateSnapshot.RealModelName,
				ReasonCode:       reasonCode,
				ReasonMessage:    virtualModelAnomalyReasonMessage(reasonCode, *candidateSnapshot),
			})
		}
	}
	return anomalies, nil
}

// judgeVirtualModelCandidateAnomaly 判定单个候选是否因被动变化不可用，返回空串表示可用喵。
// enabledModelsByGroup 是跨候选共享的分组→启模型缓存，命中过的分组不再重复查询喵。
func judgeVirtualModelCandidateAnomaly(
	candidate *model.VirtualModelCandidateSnapshot,
	ownerUserID int,
	usableGroups map[string]string,
	enabledModelsByGroup map[string]map[string]bool,
) string {
	// 自定义候选：受用户自定上游总开关约束；引用上游条目还要确认条目仍然存在喵。
	if candidate.SourceType == model.VirtualModelSourceCustom {
		// 总开关关闭时直填与引用候选一律视为不可用（与运行时跳过逻辑一致）喵。
		if !model.UserUpstreamFeatureEnabled() {
			return virtualModelAnomalyUpstreamDisabled
		}
		// 引用上游模型条目被删除后，候选凭据已悬空，运行时也会 abort，因此提前标记喵。
		if candidate.UpstreamModelID != nil && *candidate.UpstreamModelID > 0 {
			if _, loadError := model.GetUserUpstreamModelByOwnerID(*candidate.UpstreamModelID, ownerUserID); loadError != nil {
				return virtualModelAnomalyReferencedUpstreamMissing
			}
		}
		return ""
	}
	// 内部候选：与运行时 skip 逻辑同源，依次检查分组配置、auto 限制、分组访问与模型存在性喵。
	groupName := strings.TrimSpace(candidate.GroupName)
	if groupName == "" {
		return virtualModelAnomalyGroupNotConfigured
	}
	if groupName == "auto" {
		return virtualModelAnomalyAutoGroupNotSupported
	}
	// 分组降级/共享停止后，用户可用分组集合里已没有该分组，候选从调用链上摘除喵。
	if _, accessible := usableGroups[groupName]; !accessible {
		return virtualModelAnomalyGroupNotAccessible
	}
	modelsInGroup, cached := enabledModelsByGroup[groupName]
	if !cached {
		modelsInGroup = make(map[string]bool)
		for _, enabledModelName := range model.GetGroupEnabledModels(groupName) {
			modelsInGroup[enabledModelName] = true
		}
		enabledModelsByGroup[groupName] = modelsInGroup
	}
	// 节点模型被删除/共享模型停止共享后，该分组下没有此模型的可用渠道喵。
	if !modelsInGroup[strings.TrimSpace(candidate.RealModelName)] {
		return virtualModelAnomalyModelNotAvailable
	}
	return ""
}

// GetVirtualModelAnomalies 返回当前用户虚拟模型候选的被动变化异常清单喵。
// 只做只读扫描、无持久化，前端用浏览器本地已读状态与红点配合喵。
func GetVirtualModelAnomalies(c *gin.Context) {
	ownerUserID := c.GetInt("id")
	// 用户分组由认证中间件写入 context（model/user_cache.go），用于分组权限判定喵。
	userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	anomalies, scanError := scanVirtualModelCandidateAnomalies(ownerUserID, userGroup)
	if scanError != nil {
		common.ApiError(c, scanError)
		return
	}
	common.ApiSuccess(c, gin.H{"anomalies": anomalies})
}
