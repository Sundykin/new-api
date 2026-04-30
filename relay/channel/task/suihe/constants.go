package suihe

// ChannelName 用于 task 平台标识、日志与配置项。
const ChannelName = "suihe"

// 默认模型列表，可在管理端按需扩展。
// 文档示例为 seedance 系列；穗禾后端实际暴露的模型可通过 GET /v1/models 拉取，
// 此处仅作为创建渠道时的初始模型集合。
var ModelList = []string{
	"seedance-2.0",
	"seedance-2.0-fast",
}

// 缺省时长：穗禾约束 4~15 秒，未传时按 5 秒预扣费。
const defaultDurationSeconds = 5

// 画质增强基准时长：增强按"每秒处理"计价，固定计 enhanceFixedSeconds × resolution × fps 倍率。
// 5 秒为常见短视频基准；后续可通过 ModelRatio 端整体调节单价，无需改动适配器。
const enhanceFixedSeconds = 5

// 上游 API 路径常量。新增辅助接口（画质增强、任务列表、余额等）时
// 在此集中维护，避免分散字面量带来的维护成本。
const (
	PathSubmitGeneration = "/v1/videos/generations"
	PathFetchTask        = "/v1/tasks/%s"
	// 预留辅助接口路径（未启用），保留以便后续扩展时直接引用。
	PathEnhance      = "/v1/videos/enhance"
	PathTasksList    = "/v1/tasks/list"
	PathBalance      = "/v1/balance"
	PathTransactions = "/v1/transactions"
	PathModels       = "/v1/models"
	PathHealth       = "/health"
)
