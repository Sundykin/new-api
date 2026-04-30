package controller

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// 穗禾辅助接口反向代理。
//
// 这些接口（余额 / 流水 / 模型列表 / 渠道配置 / 任务列表 / 素材库 / 健康检查）
// 是无状态查询，不计费、不写入本地任务表，由网关用用户对应的穗禾渠道转发到上游。
//
// 用户素材自管能力来自上游：每个穗禾渠道（channel）持有独立的 API Key；
// 用户为自己的渠道注册令牌后，所有素材读写都自动隔离在该 Key 的命名空间下。
//
// 通道选择优先级：
//  1. Token 绑定的特定渠道（ContextKeyTokenSpecificChannelId，且类型为穗禾）；
//  2. 请求头 X-Suihe-Channel-Id（管理端调试 / 多账号切换）；
//  3. 全局首个启用状态的穗禾渠道（兜底）。

const suiheChannelHeader = "X-Suihe-Channel-Id"

// SuiheBalance 转发 GET /v1/balance。
func SuiheBalance(c *gin.Context) {
	suiheProxyJSON(c, http.MethodGet, "/v1/balance")
}

// SuiheTransactions 转发 GET /v1/transactions。
func SuiheTransactions(c *gin.Context) {
	suiheProxyJSON(c, http.MethodGet, "/v1/transactions")
}

// SuiheModels 转发 GET /v1/models。
func SuiheModels(c *gin.Context) {
	suiheProxyJSON(c, http.MethodGet, "/v1/models")
}

// SuiheChannels 转发 GET /v1/channels。
func SuiheChannels(c *gin.Context) {
	suiheProxyJSON(c, http.MethodGet, "/v1/channels")
}

// SuiheTasksList 处理 POST /v1/tasks/list。
//
// 不再走上游反向代理：上游的 task_id 与网关分发给客户端的 task_xxxx 格式不一致，
// 直接转发会导致用户看不到自己提交的任务。改为从本地任务表读取该用户在穗禾渠道下的
// 全部任务，并按穗禾文档的 { items, total } 结构返回。
//
// 请求体（与穗禾文档一致）：
//
//	{ "page": 1, "page_size": 20, "status": "success" }
//
// 字段说明：
//   - page          页码，默认 1
//   - page_size     每页条数，默认 20，最大 200
//   - status        可选；接受穗禾枚举（success / failed / pending / submitted / generating / post_processing）
//     或本地枚举（QUEUED / IN_PROGRESS / SUCCESS / FAILURE）
func SuiheTasksList(c *gin.Context) {
	var req struct {
		Page     int    `json:"page"`
		PageSize int    `json:"page_size"`
		Status   string `json:"status"`
	}
	if c.Request.ContentLength > 0 {
		if err := common.UnmarshalBodyReusable(c, &req); err != nil {
			writeSuiheError(c, http.StatusBadRequest, "invalid_request: "+err.Error())
			return
		}
	}
	page := req.Page
	if page <= 0 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}

	userId := c.GetInt("id")
	if userId == 0 {
		userId = c.GetInt(string(constant.ContextKeyUserId))
	}

	queryParams := model.SyncTaskQueryParams{
		Platform: constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeSuihe)),
		Status:   suiheStatusToLocal(req.Status),
	}

	tasks := model.TaskGetAllUserTask(userId, (page-1)*pageSize, pageSize, queryParams)
	total := model.TaskCountAllUserTask(userId, queryParams)

	items := make([]map[string]any, 0, len(tasks))
	for _, t := range tasks {
		items = append(items, mapTaskToSuiheSummary(t))
	}

	c.JSON(http.StatusOK, gin.H{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// mapTaskToSuiheSummary 将本地任务记录映射为穗禾任务摘要。
//
// 字段映射（左：本地 → 右：穗禾）：
//
//	task_id（本地公开 task_xxxx）→ task_id
//	Status                       → status（穗禾枚举）
//	Properties.OriginModelName   → model
//	Action                       → task_type（generate / enhance）
//	SubmitTime                   → created_at（Unix 秒）
//	Progress（"NN%"）            → progress / progress_pct（数值）/ progress_text
//	GetResultURL() + Data        → result_urls
//	FailReason                   → fail_reason
//	Data.source_task_id          → source_task_id（增强任务）
func mapTaskToSuiheSummary(t *model.Task) map[string]any {
	if t == nil {
		return nil
	}
	item := map[string]any{
		"task_id":    t.TaskID,
		"status":     localStatusToSuihe(t.Status),
		"model":      t.Properties.OriginModelName,
		"task_type":  taskActionToSuiheType(t.Action),
		"created_at": t.SubmitTime,
		"updated_at": t.UpdatedAt,
	}

	if pct := parseProgressPercent(t.Progress); pct >= 0 {
		item["progress_pct"] = pct
		item["progress"] = pct
		item["progress_text"] = strconv.Itoa(pct) + "%"
	} else if t.Progress != "" {
		item["progress_text"] = t.Progress
	}

	resultURLs := extractResultURLs(t)
	if len(resultURLs) > 0 {
		item["result_urls"] = resultURLs
	}
	if t.FailReason != "" {
		item["fail_reason"] = t.FailReason
	}
	if sourceID := extractSourceTaskID(t); sourceID != "" {
		item["source_task_id"] = sourceID
	}
	return item
}

// extractResultURLs 优先使用上游 result_urls 字段，回退到 PrivateData.ResultURL。
// 兼容历史数据：旧任务可能仅在 PrivateData.ResultURL 或 Task.Data.video_url 中存有地址。
func extractResultURLs(t *model.Task) []string {
	if len(t.Data) > 0 {
		var holder struct {
			ResultURLs []string `json:"result_urls"`
			VideoURL   string   `json:"video_url"`
		}
		if err := common.Unmarshal(t.Data, &holder); err == nil {
			if len(holder.ResultURLs) > 0 {
				return holder.ResultURLs
			}
			if holder.VideoURL != "" {
				return []string{holder.VideoURL}
			}
		}
	}
	if url := t.GetResultURL(); url != "" {
		return []string{url}
	}
	return nil
}

func extractSourceTaskID(t *model.Task) string {
	if len(t.Data) == 0 {
		return ""
	}
	var holder struct {
		SourceTaskID string `json:"source_task_id"`
	}
	if err := common.Unmarshal(t.Data, &holder); err == nil {
		return holder.SourceTaskID
	}
	return ""
}

// parseProgressPercent 把 "NN%" 解析为整数，失败返回 -1。
func parseProgressPercent(s string) int {
	s = strings.TrimSpace(strings.TrimSuffix(s, "%"))
	if s == "" {
		return -1
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	if n < 0 {
		return -1
	}
	return n
}

// localStatusToSuihe 把本地 TaskStatus 映射为穗禾对外枚举。
func localStatusToSuihe(s model.TaskStatus) string {
	switch s {
	case model.TaskStatusSubmitted:
		return "submitted"
	case model.TaskStatusQueued:
		return "pending"
	case model.TaskStatusInProgress:
		return "generating"
	case model.TaskStatusSuccess:
		return "success"
	case model.TaskStatusFailure:
		return "failed"
	case model.TaskStatusNotStart:
		return "submitted"
	}
	return "pending"
}

// suiheStatusToLocal 把客户端传入的状态过滤词归一化到本地 TaskStatus。
// 同时接受穗禾枚举与本地枚举（大小写不敏感），无法识别时返回空（不过滤）。
func suiheStatusToLocal(input string) string {
	v := strings.ToLower(strings.TrimSpace(input))
	switch v {
	case "":
		return ""
	case "success", "succeeded", "completed":
		return model.TaskStatusSuccess
	case "failed", "failure", "fail", "cancelled", "canceled":
		return model.TaskStatusFailure
	case "pending", "queued", "queueing", "in_queue":
		return model.TaskStatusQueued
	case "submitted":
		return model.TaskStatusSubmitted
	case "generating", "processing", "post_processing", "running", "in_progress":
		return model.TaskStatusInProgress
	case "not_start":
		return string(model.TaskStatusNotStart)
	}
	// 兜底：尝试直接匹配本地大写枚举
	upper := strings.ToUpper(strings.TrimSpace(input))
	switch upper {
	case model.TaskStatusSuccess, model.TaskStatusFailure,
		model.TaskStatusQueued, model.TaskStatusSubmitted,
		model.TaskStatusInProgress, string(model.TaskStatusNotStart):
		return upper
	}
	return ""
}

// taskActionToSuiheType 把内部 TaskAction 映射为穗禾文档的 task_type 字段。
func taskActionToSuiheType(action string) string {
	switch action {
	case constant.TaskActionEnhance:
		return "enhance"
	case constant.TaskActionTextGenerate:
		return "text_generate"
	case constant.TaskActionFirstTailGenerate:
		return "first_last_frames"
	case constant.TaskActionReferenceGenerate:
		return "reference_generate"
	case constant.TaskActionRemix:
		return "remix"
	case constant.TaskActionGenerate, "":
		return "generate"
	}
	return action
}

// SuiheTaskDetail 处理 GET /v1/tasks/{task_id}。
//
// 与任务列表一致，从本地任务表读取并按穗禾摘要结构返回，避免 task_xxxx 与上游 UUID
// 不一致。本地无该任务（可能是从外部端点直接提交、未走网关）时回退到反向代理。
func SuiheTaskDetail(c *gin.Context) {
	taskID := strings.TrimSpace(c.Param("task_id"))
	if taskID == "" {
		writeSuiheError(c, http.StatusBadRequest, "task_id_required")
		return
	}

	userId := c.GetInt("id")
	if userId == 0 {
		userId = c.GetInt(string(constant.ContextKeyUserId))
	}
	if userId > 0 {
		if t, exist, err := model.GetByTaskId(userId, taskID); err == nil && exist && t != nil {
			c.JSON(http.StatusOK, mapTaskToSuiheSummary(t))
			return
		}
	}

	suiheProxyJSON(c, http.MethodGet, "/v1/tasks/"+taskID)
}

// SuiheHealth 转发 GET /health。
func SuiheHealth(c *gin.Context) {
	suiheProxyJSON(c, http.MethodGet, "/health")
}

// SuiheAssetUpload 转发 POST /v1/asset-library/assets（multipart/form-data）。
func SuiheAssetUpload(c *gin.Context) {
	suiheProxyMultipart(c, http.MethodPost, "/v1/asset-library/assets")
}

// SuiheAssetList 转发 POST /v1/asset-library/assets/list。
func SuiheAssetList(c *gin.Context) {
	suiheProxyJSON(c, http.MethodPost, "/v1/asset-library/assets/list")
}

// SuiheAssetDelete 转发 DELETE /v1/asset-library/assets/{id}。
func SuiheAssetDelete(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		writeSuiheError(c, http.StatusBadRequest, "asset_id_required")
		return
	}
	suiheProxyJSON(c, http.MethodDelete, "/v1/asset-library/assets/"+id)
}

// suiheProxyJSON 转发非 multipart 请求（GET/POST JSON、DELETE）。
// 透传查询参数与 JSON 请求体；用渠道的 API Key 替换 Authorization。
func suiheProxyJSON(c *gin.Context, method, path string) {
	channel, err := selectSuiheChannel(c)
	if err != nil {
		writeSuiheError(c, http.StatusServiceUnavailable, err.Error())
		return
	}

	url := buildSuiheURL(channel, path, c.Request.URL.RawQuery)

	var body io.Reader
	if method == http.MethodPost && c.Request.Body != nil {
		raw, err := io.ReadAll(c.Request.Body)
		if err != nil {
			writeSuiheError(c, http.StatusBadRequest, "read_request_body_failed: "+err.Error())
			return
		}
		body = strings.NewReader(string(raw))
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), method, url, body)
	if err != nil {
		writeSuiheError(c, http.StatusInternalServerError, "build_upstream_request_failed: "+err.Error())
		return
	}
	req.Header.Set("Authorization", "Bearer "+channel.Key)
	req.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		ct := c.Request.Header.Get("Content-Type")
		if ct == "" {
			ct = "application/json"
		}
		req.Header.Set("Content-Type", ct)
	}

	doSuiheRequest(c, channel, req)
}

// suiheProxyMultipart 转发 multipart/form-data 上传请求（素材库 upload）。
// 直接以原始请求体（含 boundary）转发到上游，避免重新构造引入额外内存复制。
func suiheProxyMultipart(c *gin.Context, method, path string) {
	channel, err := selectSuiheChannel(c)
	if err != nil {
		writeSuiheError(c, http.StatusServiceUnavailable, err.Error())
		return
	}

	url := buildSuiheURL(channel, path, c.Request.URL.RawQuery)

	req, err := http.NewRequestWithContext(c.Request.Context(), method, url, c.Request.Body)
	if err != nil {
		writeSuiheError(c, http.StatusInternalServerError, "build_upstream_request_failed: "+err.Error())
		return
	}
	req.Header.Set("Authorization", "Bearer "+channel.Key)
	req.Header.Set("Accept", "application/json")
	if ct := c.Request.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	if c.Request.ContentLength > 0 {
		req.ContentLength = c.Request.ContentLength
	}
	doSuiheRequest(c, channel, req)
}

func doSuiheRequest(c *gin.Context, channel *model.Channel, req *http.Request) {
	client, err := service.GetHttpClientWithProxy(channel.GetSetting().Proxy)
	if err != nil {
		writeSuiheError(c, http.StatusInternalServerError, "init_http_client_failed: "+err.Error())
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		writeSuiheError(c, http.StatusBadGateway, "upstream_request_failed: "+err.Error())
		return
	}
	defer resp.Body.Close()

	// 透传上游 status 与响应头中的 Content-Type / Content-Length。
	for _, key := range []string{"Content-Type", "Cache-Control", "ETag", "Last-Modified"} {
		if v := resp.Header.Get(key); v != "" {
			c.Writer.Header().Set(key, v)
		}
	}
	c.Writer.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(c.Writer, resp.Body)
}

// selectSuiheChannel 按以下优先级解析穗禾渠道：
//  1. Token 绑定的特定渠道；
//  2. X-Suihe-Channel-Id 请求头；
//  3. 任意启用状态的穗禾渠道。
func selectSuiheChannel(c *gin.Context) (*model.Channel, error) {
	if v, ok := common.GetContextKey(c, constant.ContextKeyTokenSpecificChannelId); ok {
		if id, err := strconv.Atoi(fmt.Sprint(v)); err == nil {
			if ch, err := loadEnabledSuiheChannel(id); err == nil {
				return ch, nil
			}
		}
	}
	if header := strings.TrimSpace(c.GetHeader(suiheChannelHeader)); header != "" {
		id, err := strconv.Atoi(header)
		if err != nil {
			return nil, errors.New("invalid_suihe_channel_id_header")
		}
		ch, err := loadEnabledSuiheChannel(id)
		if err != nil {
			return nil, err
		}
		return ch, nil
	}

	channels, err := model.GetChannelsByType(0, 100, false, constant.ChannelTypeSuihe)
	if err != nil {
		return nil, fmt.Errorf("query_suihe_channels_failed: %w", err)
	}
	for _, ch := range channels {
		if ch == nil || ch.Status != common.ChannelStatusEnabled {
			continue
		}
		full, err := model.GetChannelById(ch.Id, true)
		if err != nil {
			continue
		}
		return full, nil
	}
	return nil, errors.New("no_enabled_suihe_channel")
}

func loadEnabledSuiheChannel(id int) (*model.Channel, error) {
	ch, err := model.GetChannelById(id, true)
	if err != nil {
		return nil, fmt.Errorf("channel_not_found: %w", err)
	}
	if ch.Type != constant.ChannelTypeSuihe {
		return nil, errors.New("channel_type_mismatch")
	}
	if ch.Status != common.ChannelStatusEnabled {
		return nil, errors.New("channel_disabled")
	}
	return ch, nil
}

func buildSuiheURL(channel *model.Channel, path, rawQuery string) string {
	base := strings.TrimRight(channel.GetBaseURL(), "/")
	if base == "" {
		base = strings.TrimRight(constant.ChannelBaseURLs[constant.ChannelTypeSuihe], "/")
	}
	url := base + path
	if rawQuery != "" {
		url += "?" + rawQuery
	}
	return url
}

func writeSuiheError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{
		"error": gin.H{
			"message": message,
			"type":    "suihe_proxy_error",
		},
	})
}
