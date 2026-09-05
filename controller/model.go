package controller

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relay/channel/ai360"
	"github.com/QuantumNous/new-api/relay/channel/lingyiwanwu"
	"github.com/QuantumNous/new-api/relay/channel/minimax"
	"github.com/QuantumNous/new-api/relay/channel/moonshot"
	"github.com/QuantumNous/new-api/relay/channel/task/meshy"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

// https://platform.openai.com/docs/api-reference/models/list

var openAIModels []dto.OpenAIModels
var openAIModelsMap map[string]dto.OpenAIModels
var channelId2Models map[int][]string

func init() {
	// https://platform.openai.com/docs/models/model-endpoint-compatibility
	for i := 0; i < constant.APITypeDummy; i++ {
		if i == constant.APITypeAIProxyLibrary {
			continue
		}
		adaptor := relay.GetAdaptor(i)
		if adaptor == nil {
			continue
		}
		channelName := adaptor.GetChannelName()
		modelNames := adaptor.GetModelList()
		for _, modelName := range modelNames {
			openAIModels = append(openAIModels, dto.OpenAIModels{
				Id:      modelName,
				Object:  "model",
				Created: 1626777600,
				OwnedBy: channelName,
			})
		}
	}
	for _, modelName := range ai360.ModelList {
		openAIModels = append(openAIModels, dto.OpenAIModels{
			Id:      modelName,
			Object:  "model",
			Created: 1626777600,
			OwnedBy: ai360.ChannelName,
		})
	}
	for _, modelName := range moonshot.ModelList {
		openAIModels = append(openAIModels, dto.OpenAIModels{
			Id:      modelName,
			Object:  "model",
			Created: 1626777600,
			OwnedBy: moonshot.ChannelName,
		})
	}
	for _, modelName := range lingyiwanwu.ModelList {
		openAIModels = append(openAIModels, dto.OpenAIModels{
			Id:      modelName,
			Object:  "model",
			Created: 1626777600,
			OwnedBy: lingyiwanwu.ChannelName,
		})
	}
	for _, modelName := range minimax.ModelList {
		openAIModels = append(openAIModels, dto.OpenAIModels{
			Id:      modelName,
			Object:  "model",
			Created: 1626777600,
			OwnedBy: minimax.ChannelName,
		})
	}
	for _, modelName := range meshy.ModelList {
		openAIModels = append(openAIModels, dto.OpenAIModels{
			Id:      modelName,
			Object:  "model",
			Created: 1626777600,
			OwnedBy: meshy.ChannelName,
		})
	}
	for modelName, _ := range constant.MidjourneyModel2Action {
		openAIModels = append(openAIModels, dto.OpenAIModels{
			Id:      modelName,
			Object:  "model",
			Created: 1626777600,
			OwnedBy: "midjourney",
		})
	}
	openAIModelsMap = make(map[string]dto.OpenAIModels)
	for _, aiModel := range openAIModels {
		openAIModelsMap[aiModel.Id] = aiModel
	}
	channelId2Models = make(map[int][]string)
	for i := 1; i <= constant.ChannelTypeDummy; i++ {
		apiType, success := common.ChannelType2APIType(i)
		if !success || apiType == constant.APITypeAIProxyLibrary {
			if plugin, ok := jsplugin.DefaultRegistry.GetByChannelType(i); ok {
				channelId2Models[i] = append([]string(nil), plugin.Meta.Models...)
			}
			continue
		}
		meta := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: i,
		}}
		adaptor := relay.GetAdaptor(apiType)
		if adaptor == nil {
			continue
		}
		adaptor.Init(meta)
		channelId2Models[i] = adaptor.GetModelList()
		if len(channelId2Models[i]) == 0 {
			if plugin, ok := jsplugin.DefaultRegistry.GetByChannelType(i); ok {
				channelId2Models[i] = append([]string(nil), plugin.Meta.Models...)
			}
		}
	}
	openAIModels = lo.UniqBy(openAIModels, func(m dto.OpenAIModels) string {
		return m.Id
	})
}

func channelOwnerName(channelType int) string {
	apiType, success := common.ChannelType2APIType(channelType)
	if !success {
		return strings.ToLower(constant.GetChannelTypeName(channelType))
	}
	adaptor := relay.GetAdaptor(apiType)
	if adaptor == nil {
		return strings.ToLower(constant.GetChannelTypeName(channelType))
	}
	adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelType: channelType,
	}})
	if name := strings.TrimSpace(adaptor.GetChannelName()); name != "" {
		return name
	}
	return strings.ToLower(constant.GetChannelTypeName(channelType))
}

func getPreferredModelOwners(modelNames []string, groups []string) map[string]string {
	channelTypes, err := model.GetPreferredModelOwnerChannelTypes(modelNames, groups)
	if err != nil {
		common.SysLog(fmt.Sprintf("GetPreferredModelOwnerChannelTypes error: %v", err))
		return map[string]string{}
	}

	ownerByChannelType := make(map[int]string)
	owners := make(map[string]string, len(channelTypes))
	for modelName, channelType := range channelTypes {
		owner, ok := ownerByChannelType[channelType]
		if !ok {
			owner = channelOwnerName(channelType)
			ownerByChannelType[channelType] = owner
		}
		if owner != "" {
			owners[modelName] = owner
		}
	}
	return owners
}

func buildOpenAIModel(modelName string, ownerByModel map[string]string) dto.OpenAIModels {
	var oaiModel dto.OpenAIModels
	if staticModel, ok := openAIModelsMap[modelName]; ok {
		oaiModel = staticModel
	} else {
		oaiModel = dto.OpenAIModels{
			Id:      modelName,
			Object:  "model",
			Created: 1626777600,
			OwnedBy: "custom",
		}
	}
	if owner, ok := ownerByModel[modelName]; ok && owner != "" {
		oaiModel.OwnedBy = owner
	}
	oaiModel.SupportedEndpointTypes = model.GetModelSupportEndpointTypes(modelName)
	return oaiModel
}

func buildVirtualOpenAIModel(virtualModel model.VirtualModel) dto.OpenAIModels {
	// 喵~防御：虚拟模型目录只返回公开名称和固定归属，不暴露候选、用户或凭据字段喵。
	createdTime := virtualModel.CreatedTime
	if createdTime <= 0 {
		createdTime = 1626777600
	}
	return dto.OpenAIModels{
		Id:      virtualModel.VirtualModelName(),
		Object:  "model",
		Created: int(createdTime),
		OwnedBy: "virtual",
	}
}

// getTokenBoundVirtualModels 读取当前用户和 API Key 已明确授权的启用虚拟模型喵。
func getTokenBoundVirtualModels(c *gin.Context) ([]model.VirtualModel, error) {
	// 喵~防御：功能关闭时不读取虚拟模型，避免控制面部署影响既有模型目录喵。
	if !model.VirtualModelFunctionEnabled() {
		return []model.VirtualModel{}, nil
	}
	ownerUserID := c.GetInt("id")
	tokenID := common.GetContextKeyInt(c, constant.ContextKeyTokenId)
	return model.GetVirtualModelsByOwnerToken(ownerUserID, tokenID)
}

func buildVirtualAnthropicModel(virtualModel model.VirtualModel) dto.AnthropicModel {
	// 喵~防御：Anthropic 目录同样只暴露公开模型名称，避免泄露内部配置喵。
	createdTime := virtualModel.CreatedTime
	if createdTime <= 0 {
		createdTime = 1626777600
	}
	return dto.AnthropicModel{
		ID:          virtualModel.VirtualModelName(),
		CreatedAt:   time.Unix(createdTime, 0).UTC().Format(time.RFC3339),
		DisplayName: virtualModel.DisplayName,
		Type:        "model",
	}
}

// modelListGroups 保存模型目录计算所需的用户组和 Token 组信息喵。
type modelListGroups struct {
	userGroup   string
	tokenGroup  string
	ownerGroups []string
}

func getModelListGroups(c *gin.Context) (modelListGroups, error) {
	tokenGroup := common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
	userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	if userGroup == "" && (tokenGroup == "" || tokenGroup == "auto") {
		var err error
		userGroup, err = model.GetUserGroup(c.GetInt("id"), false)
		if err != nil {
			return modelListGroups{}, err
		}
	}

	if tokenGroup == "auto" {
		return modelListGroups{
			userGroup:   userGroup,
			tokenGroup:  tokenGroup,
			ownerGroups: service.GetRequestAutoGroups(c, userGroup),
		}, nil
	}

	group := userGroup
	if tokenGroup != "" {
		group = tokenGroup
	}
	return modelListGroups{
		userGroup:   userGroup,
		tokenGroup:  tokenGroup,
		ownerGroups: []string{group},
	}, nil
}

// hasBillingConfigInAnyGroup 判断模型在这批分组里是否至少有一个分组配了可用价格喵。
// 分组定制定价允许「只有某个分组给这个模型定了价」，模型列表的可见性判断必须逐分组看一遍，
// 否则用户明明在自己分组里能用的模型会因为全局没定价而被过滤掉喵。
// 分组列表为空时退回全局口径判断，保持没有分组上下文时的老行为喵。
func hasBillingConfigInAnyGroup(groups []string, modelName string) bool {
	// 喵~防御：拿不到分组时按全局口径判断，避免直接把所有模型都过滤掉喵。
	if len(groups) == 0 {
		return helper.HasModelBillingConfig(modelName)
	}
	for _, group := range groups {
		if helper.HasModelBillingConfigForGroup(group, modelName) {
			return true
		}
	}
	return false
}

func ListModels(c *gin.Context, modelType int) {
	acceptUnsetRatioModel := operation_setting.SelfUseModeEnabled
	if !acceptUnsetRatioModel {
		userId := c.GetInt("id")
		if userId > 0 {
			userSettings, _ := model.GetUserSetting(userId, false)
			if userSettings.AcceptUnsetRatioModel {
				acceptUnsetRatioModel = true
			}
		}
	}

	userModelNames := make([]string, 0)
	groups, err := getModelListGroups(c)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "get user group failed",
		})
		return
	}
	ownerGroups := groups.ownerGroups
	modelLimitEnable := common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled)
	var tokenModelLimit map[string]bool
	if modelLimitEnable {
		s, ok := common.GetContextKey(c, constant.ContextKeyTokenModelLimit)
		if ok {
			tokenModelLimit, _ = s.(map[string]bool)
		}
		if tokenModelLimit == nil {
			tokenModelLimit = map[string]bool{}
		}
	}
	models := service.GetGroupsEnabledModels(ownerGroups, c.GetInt("id"))
	for _, modelName := range models {
		if modelLimitEnable {
			matchingName := ratio_setting.FormatMatchingModelName(modelName)
			if !tokenModelLimit[modelName] && !tokenModelLimit[matchingName] {
				continue
			}
		}
		// 只要该模型在用户任意一个可用分组里配了价（含分组定制价）就算已定价，
		// 否则只在某个分组定制过价的模型会被误判成未定价而从模型列表里消失喵。
		if !acceptUnsetRatioModel && !hasBillingConfigInAnyGroup(ownerGroups, modelName) {
			continue
		}
		userModelNames = append(userModelNames, modelName)
	}
	if groups.tokenGroup == "auto" {
		if value, ok := common.GetContextKey(c, constant.ContextKeyTokenAutoRoutes); ok {
			if routes, ok := value.(map[string][]string); ok {
				virtualModels := make([]string, 0, len(routes))
				for virtualModel := range routes {
					virtualModels = append(virtualModels, virtualModel)
				}
				sort.Strings(virtualModels)
				seen := make(map[string]struct{}, len(userModelNames)+len(virtualModels))
				for _, modelName := range userModelNames {
					seen[modelName] = struct{}{}
				}
				for _, virtualModel := range virtualModels {
					if _, exists := seen[virtualModel]; !exists {
						userModelNames = append(userModelNames, virtualModel)
					}
				}
			}
		}
	}

	boundVirtualModels, virtualModelError := getTokenBoundVirtualModels(c)
	boundVirtualModelByName := make(map[string]model.VirtualModel, len(boundVirtualModels))
	seenModelNames := make(map[string]struct{}, len(userModelNames)+len(boundVirtualModels))
	for _, modelName := range userModelNames {
		seenModelNames[modelName] = struct{}{}
	}
	if virtualModelError != nil {
		common.SysLog(fmt.Sprintf("GetTokenBoundVirtualModels error: %v", virtualModelError))
	} else {
		for _, virtualModel := range boundVirtualModels {
			virtualModelName := virtualModel.VirtualModelName()
			boundVirtualModelByName[virtualModelName] = virtualModel
			if _, alreadyIncluded := seenModelNames[virtualModelName]; !alreadyIncluded {
				userModelNames = append(userModelNames, virtualModelName)
				seenModelNames[virtualModelName] = struct{}{}
			}
		}
	}

	ownerByModel := map[string]string{}
	if len(ownerGroups) > 0 {
		ownerByModel = getPreferredModelOwners(userModelNames, ownerGroups)
	}
	userOpenAiModels := make([]dto.OpenAIModels, 0, len(userModelNames))
	for _, modelName := range userModelNames {
		if virtualModel, isVirtualModel := boundVirtualModelByName[modelName]; isVirtualModel {
			userOpenAiModels = append(userOpenAiModels, buildVirtualOpenAIModel(virtualModel))
			continue
		}
		userOpenAiModels = append(userOpenAiModels, buildOpenAIModel(modelName, ownerByModel))
	}

	switch modelType {
	case constant.ChannelTypeAnthropic:
		useranthropicModels := make([]dto.AnthropicModel, len(userOpenAiModels))
		for i, model := range userOpenAiModels {
			useranthropicModels[i] = dto.AnthropicModel{
				ID:          model.Id,
				CreatedAt:   time.Unix(int64(model.Created), 0).UTC().Format(time.RFC3339),
				DisplayName: model.Id,
				Type:        "model",
			}
		}
		firstID := ""
		lastID := ""
		if len(useranthropicModels) > 0 {
			firstID = useranthropicModels[0].ID
			lastID = useranthropicModels[len(useranthropicModels)-1].ID
		}
		c.JSON(200, gin.H{
			"data":     useranthropicModels,
			"first_id": firstID,
			"has_more": false,
			"last_id":  lastID,
		})
	case constant.ChannelTypeGemini:
		userGeminiModels := make([]dto.GeminiModel, len(userOpenAiModels))
		for i, model := range userOpenAiModels {
			userGeminiModels[i] = dto.GeminiModel{
				Name:        model.Id,
				DisplayName: model.Id,
			}
		}
		c.JSON(200, gin.H{
			"models":        userGeminiModels,
			"nextPageToken": nil,
		})
	default:
		c.JSON(200, gin.H{
			"success": true,
			"data":    userOpenAiModels,
			"object":  "list",
		})
	}
}

func ChannelListModels(c *gin.Context) {
	c.JSON(200, gin.H{
		"success": true,
		"data":    openAIModels,
	})
}

func DashboardListModels(c *gin.Context) {
	modelsByChannel := make(map[int][]string, len(channelId2Models))
	for channelType, models := range channelId2Models {
		modelsByChannel[channelType] = append([]string(nil), models...)
	}
	for channelType := 1; channelType <= constant.ChannelTypeDummy; channelType++ {
		if plugin, ok := jsplugin.DefaultRegistry.GetByChannelType(channelType); ok {
			modelsByChannel[channelType] = append([]string(nil), plugin.Meta.Models...)
		}
	}
	c.JSON(200, gin.H{
		"success": true,
		"data":    modelsByChannel,
	})
}

func EnabledListModels(c *gin.Context) {
	c.JSON(200, gin.H{
		"success": true,
		"data":    model.GetEnabledModels(),
	})
}

func RetrieveModel(c *gin.Context, modelType int) {
	modelID := strings.TrimPrefix(c.Param("model"), "/")
	if strings.HasPrefix(modelID, "virtual/") {
		// 喵~防御：功能关闭时不暴露虚拟模型存在性，保持数据面默认关闭喵。
		if !model.VirtualModelFunctionEnabled() {
			writeModelNotFound(c, modelID)
			return
		}
		normalizedName, normalizeError := model.NormalizeVirtualModelName(modelID)
		// 喵~防御：非法名称统一按不存在处理，避免将校验细节泄露给未授权调用方喵。
		if normalizeError != nil {
			writeModelNotFound(c, modelID)
			return
		}
		ownerUserID := c.GetInt("id")
		tokenID := common.GetContextKeyInt(c, constant.ContextKeyTokenId)
		virtualModel, queryError := model.GetEnabledVirtualModelByOwnerTokenName(ownerUserID, tokenID, normalizedName)
		// 喵~防御：未绑定、停用、跨用户和查询失败统一按不存在处理，防止资源枚举喵。
		if queryError != nil || virtualModel == nil {
			writeModelNotFound(c, modelID)
			return
		}
		virtualOpenAIModel := buildVirtualOpenAIModel(*virtualModel)
		switch modelType {
		case constant.ChannelTypeAnthropic:
			c.JSON(http.StatusOK, buildVirtualAnthropicModel(*virtualModel))
		default:
			c.JSON(http.StatusOK, virtualOpenAIModel)
		}
		return
	}
	if aiModel, ok := openAIModelsMap[modelID]; ok {
		switch modelType {
		case constant.ChannelTypeAnthropic:
			c.JSON(200, dto.AnthropicModel{
				ID:          aiModel.Id,
				CreatedAt:   time.Unix(int64(aiModel.Created), 0).UTC().Format(time.RFC3339),
				DisplayName: aiModel.Id,
				Type:        "model",
			})
		default:
			c.JSON(200, aiModel)
		}
		return
	}
	writeModelNotFound(c, modelID)
}

// writeModelNotFound 复用既有模型详情错误协议，避免资源存在性泄露喵。
func writeModelNotFound(c *gin.Context, modelID string) {
	openAIError := types.OpenAIError{
		Message: fmt.Sprintf("The model '%s' does not exist", modelID),
		Type:    "invalid_request_error",
		Param:   "model",
		Code:    "model_not_found",
	}
	c.JSON(http.StatusOK, gin.H{
		"error": openAIError,
	})
}
