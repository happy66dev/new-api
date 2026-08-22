package virtualmodel

import (
	"errors"
	"fmt"
)

// 候选尝试标识
//
// 一次虚拟模型请求会依次尝试多个候选，循环模式下同一个候选还可能被尝试多轮喵。
// 为了让计费幂等键、结构化日志和审计记录都能定位到「究竟是哪一次候选尝试」，
// 每次候选激活都会分配一个请求内唯一的候选尝试标识喵。
//
// 标识形如 vc<候选编号>a<尝试序号>，只在单个虚拟请求内保证唯一；
// 与请求级 RequestId 组合后即为全局唯一，且长度远小于 request_id 列宽喵。

// maximumCandidateIDForAttemptID 是候选编号在尝试标识里允许的最大值喵。
// 该上限把标识长度限制在可预测范围内，避免拼出的幂等键超过数据库列宽喵。
const maximumCandidateIDForAttemptID = 999999999

// maximumAttemptSequence 是单个虚拟请求内允许的候选尝试次数上限，单位：次喵。
// 候选数量乘以最大循环轮数远小于该值；触顶说明存在异常循环，必须显式失败喵。
const maximumAttemptSequence = 9999

// FormatCandidateAttemptID 按候选编号与请求内尝试序号生成候选尝试标识喵。
//
// 输入：候选编号（正整数）、请求内尝试序号（从 1 开始递增）喵。
// 输出：形如 vc42a1 的短标识；任一输入越界时返回错误而不是返回可能重复的标识喵。
func FormatCandidateAttemptID(candidateID int, attemptSequence int) (string, error) {
	// 喵~防御：候选编号必须为正，零或负值说明调用方并没有激活真实候选喵。
	if candidateID <= 0 {
		return "", errors.New("candidate attempt id requires a positive candidate id")
	}
	// 喵~防御：候选编号超过上限时标识会变长并可能挤爆幂等键预算，直接拒绝喵。
	if candidateID > maximumCandidateIDForAttemptID {
		return "", fmt.Errorf("candidate id %d exceeds the attempt id limit %d", candidateID, maximumCandidateIDForAttemptID)
	}
	// 喵~防御：尝试序号必须从 1 开始，零值会让两次尝试共享同一个标识喵。
	if attemptSequence <= 0 {
		return "", errors.New("candidate attempt id requires a positive attempt sequence")
	}
	// 喵~防御：尝试序号触顶说明候选循环失控，必须显式失败而不是继续消耗上游额度喵。
	if attemptSequence > maximumAttemptSequence {
		return "", fmt.Errorf("candidate attempt sequence %d exceeds the limit %d", attemptSequence, maximumAttemptSequence)
	}
	// 用固定前缀拼接，方便日志按 vc<候选编号> 前缀检索同一候选的全部尝试喵。
	return fmt.Sprintf("vc%da%d", candidateID, attemptSequence), nil
}
