package suihe

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

// ============================
// Request / Response structures
// ============================

// 提交视频生成响应：
//
//	{ "task_id": "<uuid>", "status": "pending", "model": "...", "created_at": "..." }
type submitResponse struct {
	TaskID    string `json:"task_id"`
	Status    string `json:"status"`
	Model     string `json:"model"`
	CreatedAt any    `json:"created_at"`
	Error     any    `json:"error,omitempty"`
}

// 任务详情响应：
//
//	{ "status": "...", "result_urls": [], "fail_reason": "...",
//	  "progress": "...", "progress_text": "...", "progress_pct": 0,
//	  "model": "...", "task_type": "..." }
//
// 字段以实际响应为准，未明确字段使用 json.RawMessage 透传。
type taskResultResponse struct {
	TaskID       string   `json:"task_id,omitempty"`
	Status       string   `json:"status"`
	ResultURLs   []string `json:"result_urls,omitempty"`
	FailReason   string   `json:"fail_reason,omitempty"`
	ProgressText string   `json:"progress_text,omitempty"`
	ProgressPct  any      `json:"progress_pct,omitempty"`
	Progress     any      `json:"progress,omitempty"`
	Model        string   `json:"model,omitempty"`
	TaskType     string   `json:"task_type,omitempty"`
	SourceTaskID string   `json:"source_task_id,omitempty"`
}

// ============================
// Adaptor implementation
// ============================

type TaskAdaptor struct {
	taskcommon.BaseBilling
	ChannelType int
	apiKey      string
	baseURL     string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = info.ChannelBaseUrl
	a.apiKey = info.ApiKey
}

// ValidateRequestAndSetAction 解析 multipart / json 请求体，校验 prompt，写入 task_request 上下文。
// 行为与其他视频渠道（vidu / doubao / jimeng）一致，便于后续扩展辅助接口时复用同一通路。
//
// 当请求路径为 PathEnhance 时进入"画质增强"分支：
//   - 不要求 prompt（增强任务作用于已有视频，无需提示词）；
//   - source_task_id 必填；
//   - info.Action 设为 TaskActionEnhance，由 BuildRequestURL 与 BuildRequestBody 分流到 enhance 路径。
func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) (taskErr *dto.TaskError) {
	if isEnhanceRequest(c) {
		return validateEnhanceRequest(c, info)
	}
	return relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate)
}

// validateEnhanceRequest 解析画质增强请求体（JSON），校验 source_task_id，写入 task_request 上下文。
func validateEnhanceRequest(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	var req relaycommon.TaskSubmitReq
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	if req.Metadata == nil {
		req.Metadata = make(map[string]any)
	}
	// 兼容客户端：source_task_id 可在顶层或 metadata 中提供。
	sourceTaskID, _ := req.Metadata["source_task_id"].(string)
	if sourceTaskID == "" {
		// 二次解析：增强请求体本身字段在 TaskSubmitReq 之外，落到 metadata 时由 raw map 解析。
		var raw map[string]any
		if err := common.UnmarshalBodyReusable(c, &raw); err == nil {
			if v, ok := raw["source_task_id"].(string); ok && v != "" {
				sourceTaskID = v
				req.Metadata["source_task_id"] = sourceTaskID
			}
			for _, key := range []string{"url_index", "tool_version", "scene", "resolution", "fps"} {
				if v, ok := raw[key]; ok {
					req.Metadata[key] = v
				}
			}
		}
	}
	if strings.TrimSpace(sourceTaskID) == "" {
		return service.TaskErrorWrapperLocal(fmt.Errorf("source_task_id is required"), "invalid_request", http.StatusBadRequest)
	}
	info.Action = constant.TaskActionEnhance
	c.Set("task_request", req)
	return nil
}

// isEnhanceRequest 判断当前请求是否为画质增强。
// 路径优先于 action 上下文，兼容内部 RouteConvert 场景。
func isEnhanceRequest(c *gin.Context) bool {
	if strings.HasSuffix(c.Request.URL.Path, PathEnhance) {
		return true
	}
	if action, ok := c.Get("action"); ok {
		if s, _ := action.(string); s == constant.TaskActionEnhance {
			return true
		}
	}
	return false
}

// EstimateBilling 返回按秒计费倍率。
//
// 标准生成任务：穗禾 duration 字段范围 4~15 秒（缺省 5 秒）。
// 画质增强任务：固定按 enhanceFixedSeconds 计费，仅取决于源视频长度的折算系数；
// 同时根据 resolution / fps 调整倍率，便于管理员单独配置每"秒-1080p-30fps"的基价。
//
// 管理员将 ModelRatio 设置为「每秒」单价即可。
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}

	if info.Action == constant.TaskActionEnhance {
		ratios := map[string]float64{
			"seconds": float64(enhanceFixedSeconds),
			"size":    resolutionRatio(stringFromMetadata(req.Metadata, "resolution")),
			"fps":     fpsRatio(intFromMetadata(req.Metadata, "fps")),
		}
		return ratios
	}

	seconds, _ := strconv.Atoi(req.Seconds)
	if seconds == 0 {
		seconds = req.Duration
	}
	if seconds <= 0 {
		seconds = defaultDurationSeconds
	}
	return map[string]float64{
		"seconds": float64(seconds),
	}
}

// BuildRequestURL 根据 action 选择上游路径。
func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	switch info.Action {
	case constant.TaskActionEnhance:
		return a.baseURL + PathEnhance, nil
	case constant.TaskActionGenerate, constant.TaskActionTextGenerate,
		constant.TaskActionFirstTailGenerate, constant.TaskActionReferenceGenerate:
		return a.baseURL + PathSubmitGeneration, nil
	default:
		return a.baseURL + PathSubmitGeneration, nil
	}
}

// BuildRequestHeader 设置鉴权 Header。
// multipart/form-data 的 Content-Type 由调用方在 BuildRequestBody 中按 boundary 重置。
// 画质增强（JSON）固定使用 application/json。
func (a *TaskAdaptor) BuildRequestHeader(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	req.Header.Set("Accept", "application/json")
	if info.Action == constant.TaskActionEnhance {
		req.Header.Set("Content-Type", "application/json")
		return nil
	}
	if ct := c.Request.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	return nil
}

// BuildRequestBody 透传 multipart 请求体，并根据模型映射改写 model 字段。
// 客户端的所有附加字段（function_mode / first_frame / frame_N / image_file_N / materials 等）
// 原样转发到穗禾上游，避免在网关层硬编码任何模式判断。
//
// 画质增强（TaskActionEnhance）走 JSON 分支，body 由 task_request.metadata 构造，
// 字段：source_task_id、url_index、tool_version、scene、resolution、fps。
func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	if info.Action == constant.TaskActionEnhance {
		req, err := relaycommon.GetTaskRequest(c)
		if err != nil {
			return nil, errors.Wrap(err, "get_enhance_request_failed")
		}
		body := buildEnhancePayload(req.Metadata)
		data, err := common.Marshal(body)
		if err != nil {
			return nil, err
		}
		c.Request.Header.Set("Content-Type", "application/json")
		return bytes.NewReader(data), nil
	}

	contentType := c.Request.Header.Get("Content-Type")

	// JSON 透传分支：在标准生成接口中并不走该分支，但保留以便扩展（例如未来支持 JSON 提交）。
	if strings.HasPrefix(contentType, "application/json") {
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			return nil, errors.Wrap(err, "get_request_body_failed")
		}
		body, err := storage.Bytes()
		if err != nil {
			return nil, errors.Wrap(err, "read_body_bytes_failed")
		}
		var bodyMap map[string]any
		if err := common.Unmarshal(body, &bodyMap); err == nil {
			bodyMap["model"] = info.UpstreamModelName
			if newBody, err := common.Marshal(bodyMap); err == nil {
				return bytes.NewReader(newBody), nil
			}
		}
		return bytes.NewReader(body), nil
	}

	if !strings.Contains(contentType, "multipart/form-data") {
		return nil, fmt.Errorf("unsupported content-type: %s", contentType)
	}

	form, err := common.ParseMultipartFormReusable(c)
	if err != nil {
		return nil, errors.Wrap(err, "parse_multipart_failed")
	}

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// 写入 model（使用上游映射后的模型名），其它文本字段原样透传。
	if err := writer.WriteField("model", info.UpstreamModelName); err != nil {
		return nil, errors.Wrap(err, "write_model_field_failed")
	}
	for key, values := range form.Value {
		if key == "model" {
			continue
		}
		for _, v := range values {
			if err := writer.WriteField(key, v); err != nil {
				return nil, errors.Wrap(err, "write_form_field_failed")
			}
		}
	}

	// 文件字段：保留原始 filename / content-type，逐个 part 复制。
	for fieldName, fileHeaders := range form.File {
		for _, fh := range fileHeaders {
			f, err := fh.Open()
			if err != nil {
				continue
			}
			ct := fh.Header.Get("Content-Type")
			if ct == "" || ct == "application/octet-stream" {
				sniff := make([]byte, 512)
				n, _ := io.ReadFull(f, sniff)
				ct = http.DetectContentType(sniff[:n])
				f.Close()
				f, err = fh.Open()
				if err != nil {
					continue
				}
			}
			h := make(textproto.MIMEHeader)
			h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, fieldName, fh.Filename))
			h.Set("Content-Type", ct)
			part, err := writer.CreatePart(h)
			if err != nil {
				f.Close()
				continue
			}
			_, _ = io.Copy(part, f)
			f.Close()
		}
	}
	if err := writer.Close(); err != nil {
		return nil, errors.Wrap(err, "close_multipart_writer_failed")
	}
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	return &buf, nil
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

// DoResponse 解析上游提交响应并返回上游 task_id。
// 客户端拿到的是网关公开的 PublicTaskID（task_xxxx），用以隔离上游真实 task_id。
func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *dto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}
	_ = resp.Body.Close()

	var sResp submitResponse
	if err := common.Unmarshal(responseBody, &sResp); err != nil {
		taskErr = service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusInternalServerError)
		return
	}

	if sResp.TaskID == "" {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("upstream returned empty task_id, body: %s", responseBody), "invalid_response", http.StatusInternalServerError)
		return
	}

	ov := dto.NewOpenAIVideo()
	ov.ID = info.PublicTaskID
	ov.TaskID = info.PublicTaskID
	ov.Model = info.OriginModelName
	ov.CreatedAt = time.Now().Unix()
	c.JSON(http.StatusOK, ov)
	return sResp.TaskID, responseBody, nil
}

// FetchTask 通过 GET /v1/tasks/{task_id} 拉取任务状态。
func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok || taskID == "" {
		return nil, fmt.Errorf("invalid task_id")
	}

	uri := fmt.Sprintf("%s"+PathFetchTask, baseUrl, taskID)
	req, err := http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	return client.Do(req)
}

func (a *TaskAdaptor) GetModelList() []string {
	return ModelList
}

func (a *TaskAdaptor) GetChannelName() string {
	return ChannelName
}

// ParseTaskResult 将穗禾上游状态映射为新 API 通用的 TaskInfo。
//
// 已知 status 枚举（与文档一致）：
//
//	pending / submitted / generating / post_processing — 处理中
//	success — 成功
//	failed  — 失败
func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	var tr taskResultResponse
	if err := common.Unmarshal(respBody, &tr); err != nil {
		return nil, errors.Wrap(err, "unmarshal task result failed")
	}

	taskInfo := &relaycommon.TaskInfo{Code: 0}

	switch strings.ToLower(strings.TrimSpace(tr.Status)) {
	case "pending", "submitted":
		taskInfo.Status = model.TaskStatusSubmitted
	case "queueing", "queued", "in_queue":
		taskInfo.Status = model.TaskStatusQueued
	case "generating", "processing", "running", "post_processing":
		taskInfo.Status = model.TaskStatusInProgress
	case "success", "succeeded", "completed":
		taskInfo.Status = model.TaskStatusSuccess
		if len(tr.ResultURLs) > 0 {
			taskInfo.Url = tr.ResultURLs[0]
		}
	case "failed", "fail", "cancelled", "canceled":
		taskInfo.Status = model.TaskStatusFailure
		taskInfo.Reason = tr.FailReason
		if taskInfo.Reason == "" {
			taskInfo.Reason = "task failed"
		}
	default:
		// 未知状态保持空，由调用方按上一次进度处理。
	}

	if p := normalizeProgress(tr.ProgressPct, tr.Progress); p != "" {
		taskInfo.Progress = p
	} else {
		switch taskInfo.Status {
		case model.TaskStatusSubmitted:
			taskInfo.Progress = taskcommon.ProgressSubmitted
		case model.TaskStatusQueued:
			taskInfo.Progress = taskcommon.ProgressQueued
		case model.TaskStatusInProgress:
			taskInfo.Progress = taskcommon.ProgressInProgress
		case model.TaskStatusSuccess, model.TaskStatusFailure:
			taskInfo.Progress = taskcommon.ProgressComplete
		}
	}

	return taskInfo, nil
}

// ConvertToOpenAIVideo 将存储的任务数据映射为 OpenAI 视频格式，用于
// GET /v1/videos/{task_id} 兼容端点。
func (a *TaskAdaptor) ConvertToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	var tr taskResultResponse
	// 任务首次提交时存储的是 submitResponse，无 result_urls；轮询后会被覆盖为 taskResultResponse。
	// 两种结构存在重叠字段（task_id / status / model），这里只读取重叠 + 轮询新增的字段。
	if err := common.Unmarshal(originTask.Data, &tr); err != nil {
		return nil, errors.Wrap(err, "unmarshal suihe task data failed")
	}

	ov := dto.NewOpenAIVideo()
	ov.ID = originTask.TaskID
	ov.TaskID = originTask.TaskID
	ov.Status = originTask.Status.ToVideoStatus()
	ov.SetProgressStr(originTask.Progress)
	ov.CreatedAt = originTask.CreatedAt
	ov.CompletedAt = originTask.UpdatedAt
	ov.Model = originTask.Properties.OriginModelName

	if len(tr.ResultURLs) > 0 {
		ov.SetMetadata("url", tr.ResultURLs[0])
		ov.SetMetadata("result_urls", tr.ResultURLs)
	}
	if tr.SourceTaskID != "" {
		ov.SetMetadata("source_task_id", tr.SourceTaskID)
	}
	if tr.TaskType != "" {
		ov.SetMetadata("task_type", tr.TaskType)
	}

	if isFailedStatus(tr.Status) {
		ov.Error = &dto.OpenAIVideoError{
			Message: tr.FailReason,
			Code:    "task_failed",
		}
	}
	return common.Marshal(ov)
}

// normalizeProgress 将上游可能的进度字段（progress_pct / progress，数值或字符串）
// 统一格式化为 "NN%"。
func normalizeProgress(pct any, progress any) string {
	if v := progressString(pct); v != "" {
		return v
	}
	return progressString(progress)
}

func progressString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return ""
		}
		if strings.HasSuffix(s, "%") {
			return s
		}
		return s + "%"
	case float64:
		return strconv.Itoa(int(x)) + "%"
	case int:
		return strconv.Itoa(x) + "%"
	case int64:
		return strconv.FormatInt(x, 10) + "%"
	}
	return ""
}

func isFailedStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "fail", "cancelled", "canceled":
		return true
	}
	return false
}

// buildEnhancePayload 根据请求 metadata 构造 enhance 上游请求体。
// 字段保持文档原名（snake_case），未提供的可选字段不写入，避免覆盖上游默认值。
func buildEnhancePayload(meta map[string]any) map[string]any {
	out := make(map[string]any, 6)
	if v := stringFromMetadata(meta, "source_task_id"); v != "" {
		out["source_task_id"] = v
	}
	if v, ok := meta["url_index"]; ok && v != nil {
		out["url_index"] = v
	}
	if v := stringFromMetadata(meta, "tool_version"); v != "" {
		out["tool_version"] = v
	}
	if v := stringFromMetadata(meta, "scene"); v != "" {
		out["scene"] = v
	}
	if v := stringFromMetadata(meta, "resolution"); v != "" {
		out["resolution"] = v
	}
	if v, ok := meta["fps"]; ok && v != nil {
		out["fps"] = v
	}
	return out
}

func stringFromMetadata(meta map[string]any, key string) string {
	if meta == nil {
		return ""
	}
	if v, ok := meta[key]; ok && v != nil {
		switch x := v.(type) {
		case string:
			return strings.TrimSpace(x)
		case fmt.Stringer:
			return strings.TrimSpace(x.String())
		}
	}
	return ""
}

func intFromMetadata(meta map[string]any, key string) int {
	if meta == nil {
		return 0
	}
	v, ok := meta[key]
	if !ok || v == nil {
		return 0
	}
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(x))
		return n
	}
	return 0
}

// resolutionRatio 将增强目标分辨率折算为计费倍率（以 1080p 为基准 1.0）。
func resolutionRatio(res string) float64 {
	switch strings.ToLower(strings.TrimSpace(res)) {
	case "720p":
		return 0.5
	case "1080p", "":
		return 1.0
	case "2k", "1440p":
		return 1.6
	case "4k", "2160p":
		return 3.0
	}
	return 1.0
}

// fpsRatio 将增强目标帧率折算为计费倍率（30fps 为 1.0）。
func fpsRatio(fps int) float64 {
	if fps <= 0 {
		return 1.0
	}
	return float64(fps) / 30.0
}
