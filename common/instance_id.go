package common

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
)

// loopGuardInstanceID 本进程唯一的实例标识，首次访问时生成并缓存喵。
// 用 sync.Once 保证并发下只生成一次，进程内全局共享喵。
var (
	loopGuardInstanceID string
	loopGuardOnce       sync.Once
)

// InstanceID 返回本进程唯一实例标识，供回环检测标记识别「本实例发出的请求」喵。
// 生成规则：16 字节密码学随机数转 32 位 hex，进程内唯一、进程重启后变化喵。
// 主人注意：进程随机意味着多副本部署各进程 ID 不同，跨副本回环无法互认；
// 单实例部署完全够用，回环必然回到同一进程喵。
func InstanceID() string {
	loopGuardOnce.Do(func() {
		randomBytes := make([]byte, 16)
		if _, readError := rand.Read(randomBytes); readError != nil {
			// 喵~防御：密码学随机源不可用时回退随机串，避免返回空 ID 导致回环检测失效喵。
			loopGuardInstanceID = GetRandomString(16)
			return
		}
		loopGuardInstanceID = hex.EncodeToString(randomBytes)
	})
	return loopGuardInstanceID
}
