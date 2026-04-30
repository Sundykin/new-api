package middleware

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// SuiheEnhanceConvert 将穗禾画质增强请求改写为新 API 通用 task 提交格式：
//
//   - 解析原始请求体 { source_task_id, url_index, tool_version, scene, resolution, fps }
//   - 通过 source_task_id 反查源任务，获取其渠道与模型，避免将增强子任务派发到不同的上游
//   - 重写请求体为 { model, prompt, metadata }，使其能通过 Distribute → RelayTask 通路
//   - 标记 action=TaskActionEnhance，由 Suihe TaskAdaptor 路由到 /v1/videos/enhance
//
// 必须在 TokenAuth 之后、Distribute 之前执行。
func SuiheEnhanceConvert() func(c *gin.Context) {
	return func(c *gin.Context) {
		var raw map[string]any
		if err := common.UnmarshalBodyReusable(c, &raw); err != nil {
			abortWithOpenAiMessage(c, http.StatusBadRequest, "invalid_request: "+err.Error())
			return
		}
		sourceTaskID := stringFromMap(raw, "source_task_id")
		if sourceTaskID == "" {
			abortWithOpenAiMessage(c, http.StatusBadRequest, "source_task_id is required")
			return
		}

		userId := c.GetInt(string(constant.ContextKeyUserId))
		if userId == 0 {
			userId = c.GetInt("id")
		}

		originTask, exist, err := model.GetByTaskId(userId, sourceTaskID)
		if err != nil {
			abortWithOpenAiMessage(c, http.StatusInternalServerError, "lookup_source_task_failed: "+err.Error())
			return
		}
		if !exist {
			abortWithOpenAiMessage(c, http.StatusBadRequest, "source_task_not_found")
			return
		}

		// 锁定源任务渠道（必须是穗禾），避免增强任务被分发到不同的上游。
		ch, err := model.GetChannelById(originTask.ChannelId, false)
		if err != nil || ch == nil {
			abortWithOpenAiMessage(c, http.StatusBadRequest, "source_channel_not_found")
			return
		}
		if ch.Type != constant.ChannelTypeSuihe {
			abortWithOpenAiMessage(c, http.StatusBadRequest, "source_task_not_from_suihe_channel")
			return
		}
		common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, strconv.Itoa(ch.Id))

		// 还原源任务模型名，便于后续 ModelMappedHelper 与计费按模型计算。
		modelName := originTask.Properties.OriginModelName
		if modelName == "" {
			modelName = originTask.Properties.UpstreamModelName
		}
		if modelName == "" {
			modelName = "seedance-2.0"
		}

		metadata := map[string]any{
			"source_task_id": sourceTaskID,
		}
		for _, key := range []string{"url_index", "tool_version", "scene", "resolution", "fps"} {
			if v, ok := raw[key]; ok && v != nil {
				metadata[key] = v
			}
		}

		unified := map[string]any{
			"model": modelName,
			// 增强任务无 prompt；填充占位符以兼容 ValidateBasicTaskRequest 的 prompt 校验路径
			// （实际不会进入该校验，Suihe.ValidateRequestAndSetAction 在 enhance 分支提前返回）。
			"prompt":   "[suihe-enhance]",
			"metadata": metadata,
		}
		jsonData, err := common.Marshal(unified)
		if err != nil {
			abortWithOpenAiMessage(c, http.StatusInternalServerError, "marshal_request_failed: "+err.Error())
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewBuffer(jsonData))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Request.ContentLength = int64(len(jsonData))
		c.Set(common.KeyRequestBody, jsonData)

		// 重写到内部统一视频提交路径，避免 Distributor 按字面 /enhance 走错路径分支。
		c.Request.URL.Path = "/v1/video/generations"
		c.Set("action", constant.TaskActionEnhance)
		c.Next()
	}
}

func stringFromMap(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}
