package model

import (
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 实体状态检测的作用域常量喵。
const (
	// EntityProbeScopeUpstream 用户上游模型自用维度喵。
	EntityProbeScopeUpstream = "upstream"
	// EntityProbeScopeUpstreamShared 用户上游模型共享调用维度喵。
	EntityProbeScopeUpstreamShared = "virtual_shared"
	// EntityProbeScopeVirtual 虚拟模型整体喵。
	EntityProbeScopeVirtual = "virtual"
	// EntityProbeScopeVirtualCandidate 虚拟模型候选节点喵。
	EntityProbeScopeVirtualCandidate = "virtual_candidate"
	// EntityProbeSharedGroupName 实体探测共享维度在 perf_metrics 表的固定分组名，与 perf_metrics 包常量对齐喵。
	EntityProbeSharedGroupName = "__entity_probe_shared__"
)

// EntityProbeState 存储单个被检测实体的最近一次调用与累计计数喵。
// 最近一次时间由本表提供（perf_metrics 小时桶太粗），可用性/延迟/24h 序列由 perf_metrics 聚合喵。
type EntityProbeState struct {
	ID            int64  `json:"id" gorm:"primaryKey"`
	Scope         string `json:"scope" gorm:"size:32;uniqueIndex:idx_eps_scope_entity,priority:1"`
	EntityID      int64  `json:"entity_id" gorm:"uniqueIndex:idx_eps_scope_entity,priority:2"`
	VirtualID     int64  `json:"virtual_id" gorm:"index;default:0"`
	OwnerUserID   int    `json:"owner_user_id" gorm:"index;default:0"`
	LastAt        int64  `json:"last_at" gorm:"bigint;default:0"`
	LastSuccess   bool   `json:"last_success" gorm:"default:false"`
	LastLatencyMs int64  `json:"last_latency_ms" gorm:"bigint;default:0"`
	LastError     string `json:"last_error" gorm:"size:512;default:''"`
	RequestCount  int64  `json:"request_count" gorm:"bigint;default:0"`
	SuccessCount  int64  `json:"success_count" gorm:"bigint;default:0"`
}

func (EntityProbeState) TableName() string {
	return "entity_probe_states"
}

// RecordEntityProbeCounted 记录一次计入成功率的真实调用结果（成功/失败都累计请求数）喵。
// 使用 OnConflict 条件更新保证并发请求原子累加，不覆盖他人已写入的计数喵。
func RecordEntityProbeCounted(scope string, entityID int64, virtualID int64, ownerUserID int, now int64, success bool, latencyMs int64, lastError string) error {
	// 喵~防御：数据库未初始化时静默跳过，状态检测是观察性副作用，绝不影响请求主流程喵。
	if DB == nil {
		return nil
	}
	// 喵~防御：作用域或实体标识无效时跳过记录，避免产生空行或歧义行喵。
	if strings.TrimSpace(scope) == "" || entityID <= 0 {
		return nil
	}
	// 只有成功才让 success_count 递增，失败保持原值喵。
	successIncrement := 0
	if success {
		successIncrement = 1
	}
	return DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "scope"}, {Name: "entity_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"last_at":         now,
			"last_success":    success,
			"last_latency_ms": latencyMs,
			"last_error":      lastError,
			"request_count":   gorm.Expr("request_count + 1"),
			"success_count":   gorm.Expr("success_count + ?", successIncrement),
		}),
	}).Create(&EntityProbeState{
		Scope:         scope,
		EntityID:      entityID,
		VirtualID:     virtualID,
		OwnerUserID:   ownerUserID,
		LastAt:        now,
		LastSuccess:   success,
		LastLatencyMs: latencyMs,
		LastError:     lastError,
		RequestCount:  1,
		SuccessCount:  int64(successIncrement),
	}).Error
}

// TouchEntityProbeLastAt 记录一次不计入成功率（配置态请求）的最近调用时间喵。
// 配置态请求（余额不足、模型停用等）只更新 last_at，不改动成功/失败计数喵。
func TouchEntityProbeLastAt(scope string, entityID int64, virtualID int64, ownerUserID int, now int64) error {
	// 喵~防御：数据库未初始化时静默跳过，状态检测是观察性副作用，绝不影响请求主流程喵。
	if DB == nil {
		return nil
	}
	// 喵~防御：作用域或实体标识无效时跳过记录喵。
	if strings.TrimSpace(scope) == "" || entityID <= 0 {
		return nil
	}
	return DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "scope"}, {Name: "entity_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"last_at": now,
		}),
	}).Create(&EntityProbeState{
		Scope:       scope,
		EntityID:    entityID,
		VirtualID:   virtualID,
		OwnerUserID: ownerUserID,
		LastAt:      now,
	}).Error
}

// GetEntityProbeState 返回指定实体的最近调用状态喵。
func GetEntityProbeState(scope string, entityID int64) (*EntityProbeState, error) {
	// 喵~防御：无效参数直接返回记录不存在喵。
	if strings.TrimSpace(scope) == "" || entityID <= 0 {
		return nil, gorm.ErrRecordNotFound
	}
	var state EntityProbeState
	if err := DB.Where("scope = ? AND entity_id = ?", scope, entityID).First(&state).Error; err != nil {
		return nil, err
	}
	return &state, nil
}

// GetEntityProbeStatesByVirtual 返回某虚拟模型下所有候选节点的状态行喵。
func GetEntityProbeStatesByVirtual(scope string, virtualID int64) ([]EntityProbeState, error) {
	var states []EntityProbeState
	// 喵~防御：无效虚拟模型标识返回空列表喵。
	if virtualID <= 0 {
		return states, nil
	}
	if err := DB.Where("scope = ? AND virtual_id = ?", scope, virtualID).Find(&states).Error; err != nil {
		return nil, err
	}
	return states, nil
}

// DeleteEntityProbeStates 删除指定实体的全部状态行（上游模型/虚拟模型删除时联动清理）喵。
func DeleteEntityProbeStates(scope string, entityID int64) error {
	// 喵~防御：无效参数直接返回，避免空条件误删全表喵。
	if strings.TrimSpace(scope) == "" || entityID <= 0 {
		return nil
	}
	return DB.Where("scope = ? AND entity_id = ?", scope, entityID).Delete(&EntityProbeState{}).Error
}

// DeleteVirtualEntityProbeStates 删除虚拟模型整体及其全部候选节点的状态行喵。
func DeleteVirtualEntityProbeStates(virtualModelID int64) error {
	// 喵~防御：无效虚拟模型标识直接返回，避免空条件误删全表喵。
	if virtualModelID <= 0 {
		return nil
	}
	// 整体行按 entity_id 匹配，候选行按 virtual_id 匹配喵。
	return DB.Where("(scope = ? AND entity_id = ?) OR (scope = ? AND virtual_id = ?)",
		EntityProbeScopeVirtual, virtualModelID, EntityProbeScopeVirtualCandidate, virtualModelID).
		Delete(&EntityProbeState{}).Error
}
