// Package values 提供各包共用的参数结构与小工具。
//
// 这里的类型是 UI ↔ sidecar 的线上契约：字段名对应 JSON-RPC 的 params/result 形状，
// 改动会影响前端，必须与 src/lib/store.js 里的调用参数保持同步。
package values

// KeyValue 是通用键值行（请求头、查询参数、gRPC metadata）。
type KeyValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ClampInt 把数值限制在 [minimum, maximum] 内；value<=0 时取 fallback。
//
// 这类"非法输入不报错、直接夹紧"的策略只用于可容忍的数值参数（超时、上限），
// 结构性错误（缺 URL、方法名非法）一律走 apperr.Invalid 明确拒绝。
func ClampInt(value, fallback, minimum, maximum int) int {
	if value <= 0 {
		value = fallback
	}
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}
