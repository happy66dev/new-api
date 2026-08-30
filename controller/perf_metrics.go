package controller

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

func GetPerfMetricsSummary(c *gin.Context) {
	hours := 24
	if rawHours := c.Query("hours"); rawHours != "" {
		if parsed, err := strconv.Atoi(rawHours); err == nil {
			hours = parsed
		}
	}

	activeGroups := append(lo.Keys(ratio_setting.GetGroupRatioCopy()), "auto")
	// 模型广场需要把共享实体探测分组纳入汇总；管理端默认不传 include_shared，保持看板干净喵。
	if v := c.Query("include_shared"); v == "1" || v == "true" {
		activeGroups = append(activeGroups, perfmetrics.EntityProbeGroupShared)
	}
	result, err := perfmetrics.QuerySummaryAll(hours, activeGroups)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

func GetPerfMetrics(c *gin.Context) {
	modelName := c.Query("model")
	if modelName == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "model is required",
		})
		return
	}

	// 喵~防御：user/<name> 共享模型需要按共享授权校验，黑名单用户不得读取共享维度可用性/延迟喵。
	trimmedModelName := strings.TrimSpace(modelName)
	if strings.HasPrefix(trimmedModelName, "user/") && !authorizePerfMetricsUpstreamModel(c, trimmedModelName) {
		// 喵~防御：未授权、不存在或黑名单命中统一返回 404，避免枚举共享模型存在性喵。
		c.JSON(http.StatusNotFound, gin.H{"success": false, "code": "model_not_found", "message": "model not found"})
		return
	}

	hours := 24
	if rawHours := c.Query("hours"); rawHours != "" {
		if parsed, err := strconv.Atoi(rawHours); err == nil {
			hours = parsed
		}
	}

	result, err := perfmetrics.Query(perfmetrics.QueryParams{
		Model: modelName,
		Group: c.Query("group"),
		Hours: hours,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	result.Groups = filterActiveGroups(result.Groups)
	for i := range result.Groups {
		// 共享实体探测分组映射为可读的 user-shared，前端 GroupBadge 已本地化"用户共享"喵。
		if result.Groups[i].Group == perfmetrics.EntityProbeGroupShared {
			result.Groups[i].Group = constant.GroupUserShared
		}
	}
	// 实时当前处理请求数：user/ 命名空间按上游模型名聚合，普通内部模型按模型名计数喵。
	if strings.HasPrefix(trimmedModelName, "user/") {
		result.CurrentRequests = middleware.GetUpstreamModelActiveCountByName(trimmedModelName)
	} else {
		result.CurrentRequests = middleware.GetInternalModelActiveCount(trimmedModelName)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

// authorizePerfMetricsUpstreamModel 校验 perf-metrics 接口对 user/<name> 模型的访问授权喵。
// 自己名下的模型总是放行；非自己名下先确认是真实共享模型，再按共享调用授权（白名单/黑名单）校验喵。
func authorizePerfMetricsUpstreamModel(c *gin.Context, modelName string) bool {
	// 喵~防御：非法模型名无法定位资源，按未授权处理喵。
	normalizedName, normalizeError := model.NormalizeUserUpstreamModelName(modelName)
	if normalizeError != nil {
		return false
	}
	viewerID := c.GetInt("id")
	// 自己名下的模型（无论是否共享）总是放行，属主可查看自己的状态维度喵。
	if _, ownError := model.GetUserUpstreamModelByOwnerName(viewerID, normalizedName); ownError == nil {
		return true
	}
	// 非自己名下：先确认这是否是一个真实开启共享的模型喵。
	_, sharedLookupError := model.GetSharedUserUpstreamModelByNormalizedName(normalizedName)
	if sharedLookupError != nil {
		// 喵~防御：非真实共享模型没有共享维度数据可泄露，按旧行为放行保持模型广场兼容喵。
		return true
	}
	// 真实共享模型：按共享调用授权校验，黑名单命中、白名单外或停用/额度耗尽按未授权处理喵。
	_, allowedError := model.GetEnabledSharedUserUpstreamModelByName(normalizedName, viewerID)
	return allowedError == nil
}

func GetPerfMetricsStatus(c *gin.Context) {
	hours := 24
	if rawHours := c.Query("hours"); rawHours != "" {
		if parsed, err := strconv.Atoi(rawHours); err == nil {
			hours = parsed
		}
	}

	activeGroups := statusCheckActiveGroups()
	var cacheExcludedModels []string
	_ = common.UnmarshalJsonStr(common.StatusCheckCacheExcludedModels, &cacheExcludedModels)
	result, err := perfmetrics.QueryStatus(hours, activeGroups, cacheExcludedModels)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

func statusCheckActiveGroups() []string {
	activeRatios := ratio_setting.GetGroupRatioCopy()
	activeGroups := make([]string, 0, len(activeRatios)+1)
	var configuredGroups []string
	_ = common.UnmarshalJsonStr(common.StatusCheckGroups, &configuredGroups)
	if len(configuredGroups) > 0 {
		seen := make(map[string]struct{}, len(configuredGroups))
		for _, group := range configuredGroups {
			if _, exists := seen[group]; exists {
				continue
			}
			if _, active := activeRatios[group]; !active && group != "auto" {
				continue
			}
			seen[group] = struct{}{}
			activeGroups = append(activeGroups, group)
		}
		return activeGroups
	}
	activeGroups = append(lo.Keys(activeRatios), "auto")
	sort.Strings(activeGroups)
	return activeGroups
}

type statusCheckFlexibleProbeGroup struct {
	Group  string
	Config perfmetrics.StatusCheckFlexibleGroupConfig
}

// statusCheckFlexibleProbeGroups deliberately requires both an explicit status
// group list and an enabled per-group configuration. An empty visible-group
// list means "show all" but must never turn every group into a billable active
// probe target.
func statusCheckFlexibleProbeGroups() []statusCheckFlexibleProbeGroup {
	var configuredGroups []string
	if err := common.UnmarshalJsonStr(common.StatusCheckGroups, &configuredGroups); err != nil || len(configuredGroups) == 0 {
		return nil
	}
	flexibleConfig := perfmetrics.GetStatusCheckFlexibleConfig()
	activeRatios := ratio_setting.GetGroupRatioCopy()
	seen := make(map[string]struct{}, len(configuredGroups))
	groups := make([]statusCheckFlexibleProbeGroup, 0, len(configuredGroups))
	for _, group := range configuredGroups {
		if group == "auto" {
			continue
		}
		if _, exists := seen[group]; exists {
			continue
		}
		if _, active := activeRatios[group]; !active {
			continue
		}
		groupConfig, enabled := flexibleConfig.EnabledGroup(group)
		if !enabled {
			continue
		}
		seen[group] = struct{}{}
		groups = append(groups, statusCheckFlexibleProbeGroup{
			Group:  group,
			Config: groupConfig,
		})
	}
	return groups
}

func filterActiveGroups(groups []perfmetrics.GroupResult) []perfmetrics.GroupResult {
	activeRatios := ratio_setting.GetGroupRatioCopy()
	return lo.Filter(groups, func(g perfmetrics.GroupResult, _ int) bool {
		// 共享实体探测分组只出现在 user/<name> 模型上，全局放行安全；
		// __entity_probe__（属主自用）绝不能放行，防止泄露属主私有用量喵。
		if g.Group == perfmetrics.EntityProbeGroupShared {
			return true
		}
		_, ok := activeRatios[g.Group]
		return ok || g.Group == "auto"
	})
}
