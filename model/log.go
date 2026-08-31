package model

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"

	"gorm.io/gorm"
)

func applyExplicitLogTextFilter(tx *gorm.DB, column string, value string) (*gorm.DB, error) {
	if value == "" {
		return tx, nil
	}
	if strings.Contains(value, "%") {
		condition, pattern, err := buildLogLikeCondition(column, value)
		if err != nil {
			return nil, err
		}
		return tx.Where(condition, pattern), nil
	}
	return tx.Where(column+" = ?", value), nil
}

func buildLogLikeCondition(column string, value string) (string, string, error) {
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		pattern, err := sanitizeClickHouseLikePattern(value)
		if err != nil {
			return "", "", err
		}
		return column + " LIKE ?", pattern, nil
	}

	pattern, err := sanitizeLikePattern(value)
	if err != nil {
		return "", "", err
	}
	return column + " LIKE ? ESCAPE '!'", pattern, nil
}

func sanitizeClickHouseLikePattern(input string) (string, error) {
	input = strings.ReplaceAll(input, `\`, `\\`)
	input = strings.ReplaceAll(input, `_`, `\_`)

	if err := validateLikePattern(input); err != nil {
		return "", err
	}
	return input, nil
}

type Log struct {
	Id                int    `json:"id" gorm:"index:idx_created_at_id,priority:2;index:idx_user_id_id,priority:2"`
	UserId            int    `json:"user_id" gorm:"index;index:idx_user_id_id,priority:1"`
	CreatedAt         int64  `json:"created_at" gorm:"bigint;index:idx_created_at_id,priority:1;index:idx_created_at_type"`
	Type              int    `json:"type" gorm:"index:idx_created_at_type"`
	Content           string `json:"content"`
	Username          string `json:"username" gorm:"index;index:index_username_model_name,priority:2;default:''"`
	TokenName         string `json:"token_name" gorm:"index;default:''"`
	ModelName         string `json:"model_name" gorm:"index;index:index_username_model_name,priority:1;default:''"`
	Quota             int    `json:"quota" gorm:"default:0"`
	PromptTokens      int    `json:"prompt_tokens" gorm:"default:0"`
	CompletionTokens  int    `json:"completion_tokens" gorm:"default:0"`
	UseTime           int    `json:"use_time" gorm:"default:0"`
	IsStream          bool   `json:"is_stream"`
	ChannelId         int    `json:"channel" gorm:"index"`
	ChannelName       string `json:"channel_name" gorm:"->"`
	TokenId           int    `json:"token_id" gorm:"default:0;index"`
	Group             string `json:"group" gorm:"index"`
	Ip                string `json:"ip" gorm:"index;default:''"`
	RequestId         string `json:"request_id,omitempty" gorm:"type:varchar(64);index:idx_logs_request_id;default:''"`
	UpstreamRequestId string `json:"upstream_request_id,omitempty" gorm:"type:varchar(128);index:idx_logs_upstream_request_id;default:''"`
	Other             string `json:"other"`
}

// don't use iota, avoid change log type value
const (
	LogTypeUnknown        = 0
	LogTypeTopup          = 1
	LogTypeConsume        = 2
	LogTypeManage         = 3
	LogTypeSystem         = 4
	LogTypeError          = 5
	LogTypeRefund         = 6
	LogTypeLogin          = 7
	LogTypeCustomUpstream = 8 // 自定上游：用户上游模型的使用日志（自用与共享都归入此类型）喵。
	LogTypeVirtualModel   = 9 // 虚拟模型：所有虚拟模型请求（internal 与 custom 候选）都归入此类型喵。
)

func ensureLogRequestId(log *Log) {
	if log != nil && log.RequestId == "" {
		log.RequestId = common.NewRequestId()
	}
}

func createLog(log *Log) error {
	ensureLogRequestId(log)
	return LOG_DB.Create(log).Error
}

func clickHouseLogOrder(prefix string) string {
	return prefix + "created_at desc, " + prefix + "request_id desc"
}

func assignDisplayLogIds(logs []*Log, startIdx int) {
	for i := range logs {
		logs[i].Id = startIdx + i + 1
	}
}

func formatUserLogs(logs []*Log, startIdx int) {
	for i := range logs {
		logs[i].ChannelName = ""
		var otherMap map[string]interface{}
		otherMap, _ = common.StrToMap(logs[i].Other)
		if otherMap != nil {
			// Remove admin-only debug fields.
			delete(otherMap, "admin_info")
			// Remove operation-audit details (operator/route info), admin-only.
			delete(otherMap, "audit_info")
			// delete(otherMap, "reject_reason")
			// delete(otherMap, "stream_status")
		}
		logs[i].Other = common.MapToJsonStr(otherMap)
	}
	assignDisplayLogIds(logs, startIdx)
}

func GetLogByTokenId(tokenId int) (logs []*Log, err error) {
	order := "id desc"
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		order = clickHouseLogOrder("")
	}
	err = LOG_DB.Model(&Log{}).Where("token_id = ?", tokenId).Order(order).Limit(common.MaxRecentItems).Find(&logs).Error
	formatUserLogs(logs, 0)
	return logs, err
}

func RecordLog(userId int, logType int, content string) {
	if logType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	username, _ := GetUsernameById(userId, false)
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      logType,
		Content:   content,
	}
	err := createLog(log)
	if err != nil {
		common.SysLog("failed to record log: " + err.Error())
	}
}

// RecordLogWithAdminInfo 记录操作日志，并将管理员相关信息存入 Other.admin_info，
func RecordLogWithAdminInfo(userId int, logType int, content string, adminInfo map[string]interface{}) {
	if logType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	username, _ := GetUsernameById(userId, false)
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      logType,
		Content:   content,
	}
	if len(adminInfo) > 0 {
		other := map[string]interface{}{
			"admin_info": adminInfo,
		}
		log.Other = common.MapToJsonStr(other)
	}
	if err := createLog(log); err != nil {
		common.SysLog("failed to record log: " + err.Error())
	}
}

// buildOpField 构建语言无关的操作描述（写入 Other.op）。
// 前端依据 action(稳定操作标识) + params(结构化参数) 在渲染期用 i18n 本地化展示，
// 因此不在数据库中存储自然语言句子。
func buildOpField(action string, params map[string]interface{}) map[string]interface{} {
	op := map[string]interface{}{
		"action": action,
	}
	if len(params) > 0 {
		op["params"] = params
	}
	return op
}

// RecordLoginLog 记录用户登录成功的审计日志（type=LogTypeLogin）。
// username 由调用方传入（登录流程已持有用户对象），避免额外的数据库查询。
// content 为英文兜底文本（用于导出）；action+params 供前端本地化渲染。
// extra 可携带 login_method、user_agent 等附加信息（普通用户可见）。
func RecordLoginLog(userId int, username string, content string, ip string, action string, params map[string]interface{}, extra map[string]interface{}) {
	other := map[string]interface{}{}
	for k, v := range extra {
		other[k] = v
	}
	other["op"] = buildOpField(action, params)
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      LogTypeLogin,
		Content:   content,
		Ip:        ip,
		Other:     common.MapToJsonStr(other),
	}
	if err := createLog(log); err != nil {
		common.SysLog("failed to record login log: " + err.Error())
	}
}

// RecordOperationAuditLog 记录管理/高危操作审计日志（type=LogTypeManage）。
// logUserId 为日志归属者：资源操作通常归操作者，针对用户的操作归目标用户。
// username 内部按 logUserId 查询。content 为英文兜底文本（供导出使用）。
// action+params 写入 Other.op，供前端本地化渲染（普通用户可见，不含敏感信息）。
// adminInfo 存放操作者身份（写入 Other.admin_info，普通用户查询时剥离）；
// auditInfo 存放路由/方法/结果等中间件兜底信息（写入 Other.audit_info，普通用户查询时剥离）。
func RecordOperationAuditLog(logUserId int, content string, ip string, action string, params map[string]interface{}, adminInfo map[string]interface{}, auditInfo map[string]interface{}) {
	username, _ := GetUsernameById(logUserId, false)
	other := map[string]interface{}{
		"op": buildOpField(action, params),
	}
	if len(adminInfo) > 0 {
		other["admin_info"] = adminInfo
	}
	if len(auditInfo) > 0 {
		other["audit_info"] = auditInfo
	}
	log := &Log{
		UserId:    logUserId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      LogTypeManage,
		Content:   content,
		Ip:        ip,
		Other:     common.MapToJsonStr(other),
	}
	if err := createLog(log); err != nil {
		common.SysLog("failed to record operation audit log: " + err.Error())
	}
}

func RecordTopupLog(userId int, content string, callerIp string, paymentMethod string, callbackPaymentMethod string) {
	username, _ := GetUsernameById(userId, false)
	adminInfo := map[string]interface{}{
		"server_ip":               common.GetIp(),
		"node_name":               common.NodeName,
		"caller_ip":               callerIp,
		"payment_method":          paymentMethod,
		"callback_payment_method": callbackPaymentMethod,
		"version":                 common.Version,
	}
	other := map[string]interface{}{
		"admin_info": adminInfo,
	}
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      LogTypeTopup,
		Content:   content,
		Ip:        callerIp,
		Other:     common.MapToJsonStr(other),
	}
	err := createLog(log)
	if err != nil {
		common.SysLog("failed to record topup log: " + err.Error())
	}
}

func RecordErrorLog(c *gin.Context, userId int, channelId int, modelName string, tokenName string, content string, tokenId int, useTimeSeconds int,
	isStream bool, group string, other map[string]interface{}) {
	logger.LogInfo(c, fmt.Sprintf("record error log: userId=%d, channelId=%d, modelName=%s, tokenName=%s, content=%s", userId, channelId, modelName, tokenName, common.LocalLogPreview(content)))
	username := c.GetString("username")
	requestId := c.GetString(common.RequestIdKey)
	upstreamRequestId := c.GetString(common.UpstreamRequestIdKey)
	otherStr := common.MapToJsonStr(other)
	// 判断是否需要记录 IP
	needRecordIp := false
	if settingMap, err := GetUserSetting(userId, false); err == nil {
		if settingMap.RecordIpLog {
			needRecordIp = true
		}
	}
	log := &Log{
		UserId:           userId,
		Username:         username,
		CreatedAt:        common.GetTimestamp(),
		Type:             LogTypeError,
		Content:          content,
		PromptTokens:     0,
		CompletionTokens: 0,
		TokenName:        tokenName,
		ModelName:        modelName,
		Quota:            0,
		ChannelId:        channelId,
		TokenId:          tokenId,
		UseTime:          useTimeSeconds,
		IsStream:         isStream,
		Group:            group,
		Ip: func() string {
			if needRecordIp {
				return c.ClientIP()
			}
			return ""
		}(),
		RequestId:         requestId,
		UpstreamRequestId: upstreamRequestId,
		Other:             otherStr,
	}
	err := createLog(log)
	if err != nil {
		logger.LogError(c, "failed to record log: "+err.Error())
	}
}

type RecordConsumeLogParams struct {
	ChannelId        int    `json:"channel_id"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	ModelName        string `json:"model_name"`
	TokenName        string `json:"token_name"`
	Quota            int    `json:"quota"`
	Content          string `json:"content"`
	TokenId          int    `json:"token_id"`
	UseTimeSeconds   int    `json:"use_time_seconds"`
	// UseTimeMs 请求级总耗时的毫秒精确值，仅虚拟模型 internal 候选注入候选尝试序列使用，普通请求为零喵。
	UseTimeMs int64 `json:"-"`
	// FirstByteMs 请求级首字耗时（毫秒），仅虚拟模型 internal 候选注入候选尝试序列使用，普通请求为零喵。
	FirstByteMs int64                  `json:"-"`
	IsStream    bool                   `json:"is_stream"`
	Group       string                 `json:"group"`
	Other       map[string]interface{} `json:"other"`
}

// InjectVirtualModelAttempts 若请求处于虚拟模型上下文，把候选尝试摘要写入 other 的 candidates 字段喵。
// 供各日志记录函数统一附加候选链故障转移过程，普通请求上下文无副作用喵。
func InjectVirtualModelAttempts(c *gin.Context, other map[string]interface{}) {
	// 喵~防御：空上下文或空 other 直接返回喵。
	if c == nil || other == nil {
		return
	}
	attempts, found := common.GetContextKeyType[*[]VirtualModelCandidateAttemptRecord](c, constant.ContextKeyVirtualCandidateAttempts)
	// 喵~防御：尝试切片未初始化时不写入，避免引入空数组噪声喵。
	if !found || attempts == nil || *attempts == nil {
		return
	}
	other["candidates"] = *attempts
}

// appendVirtualModelSuccessAttempt 在 internal 候选成功结算时把成功尝试追加到候选尝试切片喵。
// elapsedMs 与 ttftMs 分别为请求级总耗时与首字耗时（毫秒），由调用点按 relayInfo 计算喵。
func appendVirtualModelSuccessAttempt(c *gin.Context, candidateModelName string, elapsedMs int64, ttftMs int64) {
	// 喵~防御：空上下文时直接返回喵。
	if c == nil {
		return
	}
	attempts, found := common.GetContextKeyType[*[]VirtualModelCandidateAttemptRecord](c, constant.ContextKeyVirtualCandidateAttempts)
	// 喵~防御：尝试切片未初始化时静默跳过，成功记录是日志增强不阻塞主流程喵。
	if !found || attempts == nil || *attempts == nil {
		return
	}
	*attempts = append(*attempts, VirtualModelCandidateAttemptRecord{
		Seq:        common.GetContextKeyInt(c, constant.ContextKeyVirtualCandidateSeq),
		Source:     "internal",
		Label:      candidateModelName,
		Success:    true,
		StatusCode: 200,
		ElapsedMs:  elapsedMs,
		TtftMs:     ttftMs,
	})
}

// RecordConsumeLog 记录普通消费日志；虚拟模型 internal 候选请求写 type=虚拟模型 并附加候选尝试序列喵。
func RecordConsumeLog(c *gin.Context, userId int, params RecordConsumeLogParams) {
	// 喵~防御：空上下文不记录日志，避免空指针喵。
	if c == nil {
		return
	}
	if !common.LogConsumeEnabled {
		return
	}
	logger.LogInfo(c, fmt.Sprintf("record consume log: userId=%d, params=%s", userId, common.GetJsonString(params)))
	// 虚拟模型请求写 type=9 日志；渠道字段保留真实服务渠道 id，供前端据此解析渠道名喵。
	logType := LogTypeConsume
	if virtualLogType := common.GetContextKeyInt(c, constant.ContextKeyVirtualLogType); virtualLogType > 0 {
		logType = virtualLogType
		// 虚拟模型 internal 候选成功：先追加成功尝试，再注入全部候选尝试序列到 Other 喵。
		appendVirtualModelSuccessAttempt(c, params.ModelName, params.UseTimeMs, params.FirstByteMs)
		InjectVirtualModelAttempts(c, params.Other)
	}
	// 渠道字段恒为真实服务渠道 id（internal 候选即实际 relay 的 new-api 渠道），候选链序号由 candidates 序列承载喵。
	channelId := params.ChannelId
	username := c.GetString("username")
	requestId := c.GetString(common.RequestIdKey)
	upstreamRequestId := c.GetString(common.UpstreamRequestIdKey)
	createdAt := common.GetTimestamp()
	otherStr := common.MapToJsonStr(params.Other)
	// 判断是否需要记录 IP
	needRecordIp := false
	if settingMap, err := GetUserSetting(userId, false); err == nil {
		if settingMap.RecordIpLog {
			needRecordIp = true
		}
	}
	log := &Log{
		UserId:           userId,
		Username:         username,
		CreatedAt:        createdAt,
		Type:             logType,
		Content:          params.Content,
		PromptTokens:     params.PromptTokens,
		CompletionTokens: params.CompletionTokens,
		TokenName:        params.TokenName,
		ModelName:        params.ModelName,
		Quota:            params.Quota,
		ChannelId:        channelId,
		TokenId:          params.TokenId,
		UseTime:          params.UseTimeSeconds,
		IsStream:         params.IsStream,
		Group:            params.Group,
		Ip: func() string {
			if needRecordIp {
				return c.ClientIP()
			}
			return ""
		}(),
		RequestId:         requestId,
		UpstreamRequestId: upstreamRequestId,
		Other:             otherStr,
	}
	err := createLog(log)
	if err != nil {
		logger.LogError(c, "failed to record log: "+err.Error())
	}
	if common.DataExportEnabled {
		LogQuotaData(QuotaDataLogParams{
			UserID:    userId,
			Username:  username,
			ModelName: params.ModelName,
			Quota:     params.Quota,
			CreatedAt: createdAt,
			TokenUsed: params.PromptTokens + params.CompletionTokens,
			UseGroup:  params.Group,
			TokenID:   params.TokenId,
			ChannelID: params.ChannelId,
			NodeName:  common.NodeName,
		})
	}
}

// RecordUserUpstreamModelLogParams 描述自定上游日志的可写字段喵。
type RecordUserUpstreamModelLogParams struct {
	PromptTokens     int                    `json:"prompt_tokens"`
	CompletionTokens int                    `json:"completion_tokens"`
	ModelName        string                 `json:"model_name"`
	TokenName        string                 `json:"token_name"`
	TokenId          int                    `json:"token_id"`
	Content          string                 `json:"content"`
	UseTimeSeconds   int                    `json:"use_time_seconds"`
	IsStream         bool                   `json:"is_stream"`
	Group            string                 `json:"group"`
	Other            map[string]interface{} `json:"other"`
}

// RecordUserUpstreamModelLog 记录用户上游模型的独立计费日志（type=自定上游）喵。
// 共享调用与自用调用都归入该类型，通过 Group 字段区分（共享=user-shared）喵。
// 虚拟模型 custom 候选引用上游模型时，上下文标记虚拟模型日志类型则写 type=虚拟模型喵。
func RecordUserUpstreamModelLog(c *gin.Context, userId int, params RecordUserUpstreamModelLogParams) {
	// 喵~防御：空上下文或空模型名不写日志，避免脏数据喵。
	if c == nil || params.ModelName == "" {
		return
	}
	logType := LogTypeCustomUpstream
	if virtualLogType := common.GetContextKeyInt(c, constant.ContextKeyVirtualLogType); virtualLogType > 0 {
		logType = virtualLogType
		// 虚拟模型 custom 候选引用上游模型时附加候选尝试序列喵。
		InjectVirtualModelAttempts(c, params.Other)
	}
	username := c.GetString("username")
	requestId := c.GetString(common.RequestIdKey)
	createdAt := common.GetTimestamp()
	otherStr := common.MapToJsonStr(params.Other)
	log := &Log{
		UserId:           userId,
		Username:         username,
		CreatedAt:        createdAt,
		Type:             logType,
		Content:          params.Content,
		PromptTokens:     params.PromptTokens,
		CompletionTokens: params.CompletionTokens,
		TokenName:        params.TokenName,
		ModelName:        params.ModelName,
		// 独立 RMB 计费系统不走 new-api quota，Quota 固定为 0 喵。
		Quota:     0,
		TokenId:   params.TokenId,
		UseTime:   params.UseTimeSeconds,
		IsStream:  params.IsStream,
		Group:     params.Group,
		RequestId: requestId,
		Other:     otherStr,
	}
	if err := createLog(log); err != nil {
		logger.LogError(c, "failed to record user upstream model log: "+err.Error())
	}
}

// RecordVirtualModelLogParams 描述虚拟模型日志的可写字段喵。
type RecordVirtualModelLogParams struct {
	PromptTokens     int                    `json:"prompt_tokens"`
	CompletionTokens int                    `json:"completion_tokens"`
	ModelName        string                 `json:"model_name"`
	TokenName        string                 `json:"token_name"`
	TokenId          int                    `json:"token_id"`
	Content          string                 `json:"content"`
	UseTimeSeconds   int                    `json:"use_time_seconds"`
	IsStream         bool                   `json:"is_stream"`
	Group            string                 `json:"group"`
	Quota            int                    `json:"quota"`
	Other            map[string]interface{} `json:"other"`
}

// RecordVirtualModelLog 记录虚拟模型请求日志（type=虚拟模型）喵。
// internal 候选走原生消费日志（type 覆盖为 9），此函数服务于 custom 候选（引用上游或纯直填）喵。
func RecordVirtualModelLog(c *gin.Context, userId int, params RecordVirtualModelLogParams) {
	// 喵~防御：空上下文或空模型名不写日志，避免脏数据喵。
	if c == nil || params.ModelName == "" {
		return
	}
	// 附加候选尝试序列，供日志详情展示候选链故障转移过程喵。
	InjectVirtualModelAttempts(c, params.Other)
	username := c.GetString("username")
	requestId := c.GetString(common.RequestIdKey)
	createdAt := common.GetTimestamp()
	otherStr := common.MapToJsonStr(params.Other)
	log := &Log{
		UserId:           userId,
		Username:         username,
		CreatedAt:        createdAt,
		Type:             LogTypeVirtualModel,
		Content:          params.Content,
		PromptTokens:     params.PromptTokens,
		CompletionTokens: params.CompletionTokens,
		TokenName:        params.TokenName,
		ModelName:        params.ModelName,
		Quota:            params.Quota,
		TokenId:          params.TokenId,
		UseTime:          params.UseTimeSeconds,
		IsStream:         params.IsStream,
		Group:            params.Group,
		RequestId:        requestId,
		Other:            otherStr,
	}
	if err := createLog(log); err != nil {
		logger.LogError(c, "failed to record virtual model log: "+err.Error())
	}
}

type RecordTaskBillingLogParams struct {
	UserId    int
	LogType   int
	Content   string
	ChannelId int
	ModelName string
	Quota     int
	TokenId   int
	Group     string
	Other     map[string]interface{}
	NodeName  string // 任务发起节点；为空时回退当前节点
}

func RecordTaskBillingLog(params RecordTaskBillingLogParams) {
	if params.LogType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	username, _ := GetUsernameById(params.UserId, false)
	tokenName := ""
	if params.TokenId > 0 {
		if token, err := GetTokenById(params.TokenId); err == nil {
			tokenName = token.Name
		}
	}
	createdAt := common.GetTimestamp()
	log := &Log{
		UserId:    params.UserId,
		Username:  username,
		CreatedAt: createdAt,
		Type:      params.LogType,
		Content:   params.Content,
		TokenName: tokenName,
		ModelName: params.ModelName,
		Quota:     params.Quota,
		ChannelId: params.ChannelId,
		TokenId:   params.TokenId,
		Group:     params.Group,
		Other:     common.MapToJsonStr(params.Other),
	}
	err := createLog(log)
	if err != nil {
		common.SysLog("failed to record task billing log: " + err.Error())
	}
	if params.LogType == LogTypeConsume && common.DataExportEnabled {
		nodeName := params.NodeName
		if nodeName == "" {
			nodeName = common.NodeName
		}
		LogQuotaData(QuotaDataLogParams{
			UserID:    params.UserId,
			Username:  username,
			ModelName: params.ModelName,
			Quota:     params.Quota,
			CreatedAt: createdAt,
			UseGroup:  params.Group,
			TokenID:   params.TokenId,
			ChannelID: params.ChannelId,
			NodeName:  nodeName,
		})
	}
}

func GetAllLogs(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, startIdx int, num int, channel int, group string, requestId string, upstreamRequestId string) (logs []*Log, total int64, err error) {
	var tx *gorm.DB
	if logType == LogTypeUnknown {
		tx = LOG_DB
	} else {
		tx = LOG_DB.Where("logs.type = ?", logType)
	}

	if tx, err = applyExplicitLogTextFilter(tx, "logs.model_name", modelName); err != nil {
		return nil, 0, err
	}
	if tx, err = applyExplicitLogTextFilter(tx, "logs.username", username); err != nil {
		return nil, 0, err
	}
	if tokenName != "" {
		tx = tx.Where("logs.token_name = ?", tokenName)
	}
	if requestId != "" {
		tx = tx.Where("logs.request_id = ?", requestId)
	}
	if upstreamRequestId != "" {
		tx = tx.Where("logs.upstream_request_id = ?", upstreamRequestId)
	}
	if startTimestamp != 0 {
		tx = tx.Where("logs.created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("logs.created_at <= ?", endTimestamp)
	}
	if channel != 0 {
		tx = tx.Where("logs.channel_id = ?", channel)
	}
	if group != "" {
		tx = tx.Where("logs."+logGroupCol+" = ?", group)
	}
	err = tx.Model(&Log{}).Count(&total).Error
	if err != nil {
		return nil, 0, err
	}
	order := "logs.created_at desc, logs.id desc"
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		order = clickHouseLogOrder("logs.")
	}
	err = tx.Order(order).Limit(num).Offset(startIdx).Find(&logs).Error
	if err != nil {
		return nil, 0, err
	}
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		assignDisplayLogIds(logs, startIdx)
	}

	channelIds := types.NewSet[int]()
	for _, log := range logs {
		if log.ChannelId != 0 {
			channelIds.Add(log.ChannelId)
		}
	}

	if channelIds.Len() > 0 {
		var channels []struct {
			Id   int    `gorm:"column:id"`
			Name string `gorm:"column:name"`
		}
		if common.MemoryCacheEnabled {
			// Cache get channel
			for _, channelId := range channelIds.Items() {
				if cacheChannel, err := CacheGetChannel(channelId); err == nil {
					channels = append(channels, struct {
						Id   int    `gorm:"column:id"`
						Name string `gorm:"column:name"`
					}{
						Id:   channelId,
						Name: cacheChannel.Name,
					})
				}
			}
		} else {
			// Bulk query channels from DB
			if err = DB.Table("channels").Select("id, name").Where("id IN ?", channelIds.Items()).Find(&channels).Error; err != nil {
				return logs, total, err
			}
		}
		channelMap := make(map[int]string, len(channels))
		for _, channel := range channels {
			channelMap[channel.Id] = channel.Name
		}
		for i := range logs {
			logs[i].ChannelName = channelMap[logs[i].ChannelId]
		}
	}

	return logs, total, err
}

const logSearchCountLimit = 10000

func GetUserLogs(userId int, sharedModelNames []string, logType int, startTimestamp int64, endTimestamp int64, modelName string, tokenName string, startIdx int, num int, group string, requestId string, upstreamRequestId string) (logs []*Log, total int64, err error) {
	var tx *gorm.DB
	// 喵~防御：共享模型名集合非空时按「全部」范围查询：自己的调用（按所选类型过滤）+ 别人调用我的共享模型（user-shared 分组、type=8）喵。
	if len(sharedModelNames) > 0 {
		if logType == LogTypeUnknown {
			// 「全部」类型：自己的任意类型调用与共享被调日志取并集，共享被调恒为 type=8 喵。
			tx = LOG_DB.Where("(logs.user_id = ?) OR (logs.model_name IN ? AND logs."+logGroupCol+" = ? AND logs.type = ?)", userId, sharedModelNames, constant.GroupUserShared, LogTypeCustomUpstream)
		} else {
			// 指定类型：自己的该类型调用 + 共享被调日志（恒为 type=8），保证筛选与统计口径一致喵。
			tx = LOG_DB.Where("(logs.user_id = ? AND logs.type = ?) OR (logs.model_name IN ? AND logs."+logGroupCol+" = ? AND logs.type = ?)", userId, logType, sharedModelNames, constant.GroupUserShared, LogTypeCustomUpstream)
		}
	} else if logType == LogTypeUnknown {
		tx = LOG_DB.Where("logs.user_id = ?", userId)
	} else {
		tx = LOG_DB.Where("logs.user_id = ? and logs.type = ?", userId, logType)
	}

	if tx, err = applyExplicitLogTextFilter(tx, "logs.model_name", modelName); err != nil {
		return nil, 0, err
	}
	if tokenName != "" {
		tx = tx.Where("logs.token_name = ?", tokenName)
	}
	if requestId != "" {
		tx = tx.Where("logs.request_id = ?", requestId)
	}
	if upstreamRequestId != "" {
		tx = tx.Where("logs.upstream_request_id = ?", upstreamRequestId)
	}
	if startTimestamp != 0 {
		tx = tx.Where("logs.created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("logs.created_at <= ?", endTimestamp)
	}
	if group != "" {
		tx = tx.Where("logs."+logGroupCol+" = ?", group)
	}
	err = tx.Model(&Log{}).Limit(logSearchCountLimit).Count(&total).Error
	if err != nil {
		common.SysError("failed to count user logs: " + err.Error())
		return nil, 0, errors.New("查询日志失败")
	}
	order := "logs.id desc"
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		order = clickHouseLogOrder("logs.")
	}
	err = tx.Order(order).Limit(num).Offset(startIdx).Find(&logs).Error
	if err != nil {
		common.SysError("failed to search user logs: " + err.Error())
		return nil, 0, errors.New("查询日志失败")
	}

	formatUserLogs(logs, startIdx)
	return logs, total, err
}

type Stat struct {
	Quota int `json:"quota"`
	Rpm   int `json:"rpm"`
	Tpm   int `json:"tpm"`
}

// GetTokenRPM returns consume-log counts for the last minute in one grouped
// query. Keeping this grouped avoids an N+1 query when rendering the key list.
func GetTokenRPM(tokenIDs []int) (map[int]int, error) {
	result := make(map[int]int, len(tokenIDs))
	if len(tokenIDs) == 0 {
		return result, nil
	}
	for _, tokenID := range tokenIDs {
		result[tokenID] = 0
	}
	if LOG_DB == nil {
		return result, errors.New("日志数据库未初始化")
	}
	var rows []struct {
		TokenID int `gorm:"column:token_id"`
		RPM     int `gorm:"column:rpm"`
	}
	err := LOG_DB.Table("logs").
		Select("token_id, COUNT(*) AS rpm").
		Where("type = ? AND created_at >= ? AND token_id IN ?", LogTypeConsume, time.Now().Add(-60*time.Second).Unix(), tokenIDs).
		Group("token_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.TokenID] = row.RPM
	}
	return result, nil
}

// GetUserTokenRPM limits token RPM results to tokens owned by the user.
func GetUserTokenRPM(userID int, tokenIDs []int) (map[int]int, error) {
	result := make(map[int]int, len(tokenIDs))
	if userID <= 0 || len(tokenIDs) == 0 {
		return result, nil
	}
	if DB == nil {
		return result, errors.New("主数据库未初始化")
	}
	ownedTokenIDs := make([]int, 0, len(tokenIDs))
	if err := DB.Model(&Token{}).
		Where("user_id = ? AND id IN ?", userID, tokenIDs).
		Pluck("id", &ownedTokenIDs).Error; err != nil {
		return nil, err
	}
	return GetTokenRPM(ownedTokenIDs)
}

func SumUsedQuota(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, channel int, group string, sharedModelNames ...string) (stat Stat, err error) {
	// quota 求和与 rpm/tpm 统计分开查询，因为 rpm/tpm 只统计最近 60 秒的调用量喵。
	quotaQuery := LOG_DB.Table("logs").Select("COALESCE(sum(quota), 0) quota")
	rpmTpmQuery := LOG_DB.Table("logs").Select("count(*) rpm, COALESCE(sum(prompt_tokens), 0) + COALESCE(sum(completion_tokens), 0) tpm")

	// 普通用户「全部」范围：自己的消费日志 + 别人调用自己共享模型的消费日志（user-shared 分组、type=8）取并集喵。
	if len(sharedModelNames) > 0 {
		// 喵~防御：type 条件内嵌进 OR 表达式，避免外层的 type=消费 误杀共享被调日志喵。
		if logType == LogTypeUnknown {
			// 「全部」类型：自己的任意类型调用与共享被调日志取并集，与 GetUserLogs 列表口径一致喵。
			scopeCondition := "(username = ?) OR (model_name IN ? AND " + logGroupCol + " = ? AND type = ?)"
			quotaQuery = quotaQuery.Where(scopeCondition, username, sharedModelNames, constant.GroupUserShared, LogTypeCustomUpstream)
			rpmTpmQuery = rpmTpmQuery.Where(scopeCondition, username, sharedModelNames, constant.GroupUserShared, LogTypeCustomUpstream)
		} else {
			// 指定类型：自己的该类型调用 + 共享被调日志（恒为 type=8），与 GetUserLogs 列表口径一致喵。
			scopeCondition := "(username = ? AND type = ?) OR (model_name IN ? AND " + logGroupCol + " = ? AND type = ?)"
			quotaQuery = quotaQuery.Where(scopeCondition, username, logType, sharedModelNames, constant.GroupUserShared, LogTypeCustomUpstream)
			rpmTpmQuery = rpmTpmQuery.Where(scopeCondition, username, logType, sharedModelNames, constant.GroupUserShared, LogTypeCustomUpstream)
		}
	} else {
		// 其余范围（管理员全量 / 仅自己）：按用户名过滤喵。
		if quotaQuery, err = applyExplicitLogTextFilter(quotaQuery, "username", username); err != nil {
			return stat, err
		}
		if rpmTpmQuery, err = applyExplicitLogTextFilter(rpmTpmQuery, "username", username); err != nil {
			return stat, err
		}
	}
	if tokenName != "" {
		quotaQuery = quotaQuery.Where("token_name = ?", tokenName)
		rpmTpmQuery = rpmTpmQuery.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		quotaQuery = quotaQuery.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		quotaQuery = quotaQuery.Where("created_at <= ?", endTimestamp)
	}
	if quotaQuery, err = applyExplicitLogTextFilter(quotaQuery, "model_name", modelName); err != nil {
		return stat, err
	}
	if rpmTpmQuery, err = applyExplicitLogTextFilter(rpmTpmQuery, "model_name", modelName); err != nil {
		return stat, err
	}
	if channel != 0 {
		quotaQuery = quotaQuery.Where("channel_id = ?", channel)
		rpmTpmQuery = rpmTpmQuery.Where("channel_id = ?", channel)
	}
	if group != "" {
		quotaQuery = quotaQuery.Where(logGroupCol+" = ?", group)
		rpmTpmQuery = rpmTpmQuery.Where(logGroupCol+" = ?", group)
	}

	// 「全部」范围的 type 已内嵌进 OR 条件；其余范围统一限定消费日志喵。
	if len(sharedModelNames) == 0 {
		quotaQuery = quotaQuery.Where("type = ?", LogTypeConsume)
		rpmTpmQuery = rpmTpmQuery.Where("type = ?", LogTypeConsume)
	}

	// 只统计最近60秒的rpm和tpm
	rpmTpmQuery = rpmTpmQuery.Where("created_at >= ?", time.Now().Add(-60*time.Second).Unix())

	// 执行查询
	if err := quotaQuery.Scan(&stat).Error; err != nil {
		common.SysError("failed to query log stat: " + err.Error())
		return stat, errors.New("查询统计数据失败")
	}
	if err := rpmTpmQuery.Scan(&stat).Error; err != nil {
		common.SysError("failed to query rpm/tpm stat: " + err.Error())
		return stat, errors.New("查询统计数据失败")
	}

	return stat, nil
}

func SumUsedToken(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string) (token int) {
	tx := LOG_DB.Table("logs").Select("COALESCE(sum(prompt_tokens), 0) + COALESCE(sum(completion_tokens), 0)")
	if username != "" {
		tx = tx.Where("username = ?", username)
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if modelName != "" {
		tx = tx.Where("model_name = ?", modelName)
	}
	tx.Where("type = ?", LogTypeConsume).Scan(&token)
	return token
}

func CountOldLog(ctx context.Context, targetTimestamp int64) (int64, error) {
	var total int64
	if err := LOG_DB.WithContext(ctx).Model(&Log{}).Where("created_at < ?", targetTimestamp).Count(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

func DeleteOldLogBatch(ctx context.Context, targetTimestamp int64, limit int) (int64, error) {
	if limit <= 0 {
		limit = 100
	}
	if nil != ctx.Err() {
		return 0, ctx.Err()
	}

	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		// ClickHouse DELETE is a heavy mutation that rewrites data parts, so
		// per-batch mutations would be pathologically slow. Remove all matching
		// rows in a single synchronous mutation regardless of limit; the reported
		// count lets the caller's progress loop complete in one pass.
		total, err := CountOldLog(ctx, targetTimestamp)
		if err != nil {
			return 0, err
		}
		if total == 0 {
			return 0, nil
		}
		if err := LOG_DB.WithContext(ctx).Exec(
			"ALTER TABLE logs DELETE WHERE created_at < ? SETTINGS mutations_sync = 1",
			targetTimestamp,
		).Error; err != nil {
			return 0, err
		}
		return total, nil
	}

	result := LOG_DB.WithContext(ctx).Where("created_at < ?", targetTimestamp).Limit(limit).Delete(&Log{})
	if nil != result.Error {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}
