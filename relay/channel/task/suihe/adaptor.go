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

// ValidateRequestAndSetAction 解析请求体并写入 task_request 上下文。
//
// 标准生成（POST /v1/videos 或 /suihe/v1/videos/generations）：对外只接 OpenAI 视频 API
// 标准字段（prompt / model / seconds / size / input_reference），与 sora 等其它视频渠道一致；
// 上游真正用到的 suihe 字段（duration / ratio / video_resolution / first_frame 等）由
// BuildRequestBody 统一转换。复用 ValidateMultipartDirect 完成 prompt + model 校验。
//
// 画质增强（PathEnhance）走独立分支：
//   - 不要求 prompt；
//   - source_task_id 必填；
//   - info.Action = TaskActionEnhance，由 BuildRequestURL/BuildRequestBody 分流到 enhance 路径。
func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) (taskErr *dto.TaskError) {
	if isEnhanceRequest(c) {
		return validateEnhanceRequest(c, info)
	}
	return relaycommon.ValidateMultipartDirect(c, info)
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

// BuildRequestBody 把 OpenAI 标准视频请求转换为穗禾上游文档定义的 multipart 字段。
//
// 输入（OpenAI 兼容，由 ValidateMultipartDirect 解析到 TaskSubmitReq）：
//   - prompt / model / seconds / size / input_reference (file)
//   - metadata: 透传到上游 suihe 文档里的扩展字段（function_mode / ratio / video_resolution /
//     end_frame_url / channel / materials 等），便于客户端访问 suihe 高级模式。
//
// 输出（穗禾 /v1/videos/generations multipart）：
//   - prompt / model / duration / video_resolution / ratio
//   - first_frame：来源于 multipart 中的 input_reference 文件（如果有）
//   - 其它扩展字段：透传 metadata 中的字符串值。
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

	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, errors.Wrap(err, "get_request_failed")
	}

	duration := req.Duration
	if duration == 0 && req.Seconds != "" {
		duration, _ = strconv.Atoi(req.Seconds)
	}
	if duration <= 0 {
		duration = defaultDurationSeconds
	}

	_, ratio := mapOpenAISizeToSuihe(req.Size)
	// 当前阶段穗禾渠道仅放开 480p 档位（与商务策略一致），上游分辨率统一锁定，
	// 不接受 metadata.video_resolution 覆盖，避免越权选择更高码率。
	resolution := suiheLockedResolution
	if v := stringFromMetadata(req.Metadata, "ratio"); v != "" {
		ratio = v
	}

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	if err := writer.WriteField("prompt", req.Prompt); err != nil {
		return nil, errors.Wrap(err, "write_prompt_failed")
	}
	if err := writer.WriteField("model", info.UpstreamModelName); err != nil {
		return nil, errors.Wrap(err, "write_model_failed")
	}
	if err := writer.WriteField("duration", strconv.Itoa(duration)); err != nil {
		return nil, errors.Wrap(err, "write_duration_failed")
	}
	if resolution != "" {
		_ = writer.WriteField("video_resolution", resolution)
	}
	if ratio != "" {
		_ = writer.WriteField("ratio", ratio)
	}

	// 透传 metadata 里的扩展字段：function_mode / channel / materials / end_frame_url 等。
	// 已显式映射的字段（video_resolution / ratio）在上面已处理，这里跳过。
	for key, value := range req.Metadata {
		switch key {
		case "video_resolution", "ratio", "duration", "prompt", "model":
			continue
		}
		if value == nil {
			continue
		}
		// metadata 仅透传字符串/数值类型，复杂结构（map/数组）需要 JSON 序列化。
		switch v := value.(type) {
		case string:
			if v != "" {
				_ = writer.WriteField(key, v)
			}
		case int, int64, float64, bool:
			_ = writer.WriteField(key, fmt.Sprint(v))
		default:
			if encoded, err := common.Marshal(v); err == nil {
				_ = writer.WriteField(key, string(encoded))
			}
		}
	}

	// 把客户端 multipart 中的 input_reference 文件改名为 suihe 文档里的 first_frame，
	// 与 OpenAI 视频 API 中"参考图首帧"的语义一致。
	if strings.HasPrefix(c.Request.Header.Get("Content-Type"), "multipart/form-data") {
		if form, err := common.ParseMultipartFormReusable(c); err == nil {
			if files := form.File["input_reference"]; len(files) > 0 {
				if err := writeMultipartFile(writer, "first_frame", files[0]); err != nil {
					return nil, errors.Wrap(err, "write_first_frame_failed")
				}
			}
			// 客户端在多帧/全能引用模式下可直接以 suihe 文档命名上传文件
			// （end_frame、frame_1、image_file_1、video_file_1、audio_file_1 等），透传不改名。
			for fieldName, fhs := range form.File {
				if fieldName == "input_reference" {
					continue
				}
				for _, fh := range fhs {
					if err := writeMultipartFile(writer, fieldName, fh); err != nil {
						return nil, errors.Wrap(err, "write_extra_file_failed")
					}
				}
			}
		}
	}

	// JSON 提交路径下，OpenAI 标准用 input_reference / images: [url] 表达首帧/参考图。
	// 穗禾上游只接 multipart 文件，网关在此把 URL 拉下来转成 multipart part。
	// 字段命名按 metadata.function_mode 分流（与 suihe 文档对齐）：
	//
	//   omni_reference   →  全部 images 都是 image_file_1, image_file_2, ...
	//                      （全能引用：每张图都是参考素材，没有"首帧"概念）
	//   multi_frame      →  images[0] = first_frame, images[1..] = frame_1, frame_2, ...
	//   first_last_frames →  images[0] = first_frame；end_frame 走 metadata.end_frame_url 透传
	//   first_frame / 其他 →  默认：images[0] = first_frame, images[1..] = frame_1, frame_2, ...
	//
	// 上游对字段名的语义敏感：function_mode=omni_reference 但字段是 first_frame/frame_N，
	// 上游会按多帧模式处理（只看 first_frame，剩下的引导帧），导致客户端"全能引用"语义丢失。
	imageURLs := collectImageURLs(req)
	if len(imageURLs) > 0 {
		functionMode := stringFromMetadata(req.Metadata, "function_mode")
		switch functionMode {
		case "omni_reference":
			for i, u := range imageURLs {
				fieldName := fmt.Sprintf("image_file_%d", i+1)
				if err := writeRemoteImageAsPart(writer, fieldName, u); err != nil {
					return nil, errors.Wrapf(err, "fetch_%s_failed", fieldName)
				}
			}
		default:
			// first_frame / multi_frame / first_last_frames / 未声明：保持原默认行为
			if err := writeRemoteImageAsPart(writer, "first_frame", imageURLs[0]); err != nil {
				return nil, errors.Wrap(err, "fetch_first_frame_failed")
			}
			for i, u := range imageURLs[1:] {
				fieldName := fmt.Sprintf("frame_%d", i+1)
				if err := writeRemoteImageAsPart(writer, fieldName, u); err != nil {
					return nil, errors.Wrapf(err, "fetch_%s_failed", fieldName)
				}
			}
		}
	}

	if err := writer.Close(); err != nil {
		return nil, errors.Wrap(err, "close_multipart_writer_failed")
	}
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	return &buf, nil
}

// collectImageURLs 汇总 OpenAI 标准请求中的远程图片 URL（input_reference / images），
// 调用方按顺序映射为 first_frame、frame_1、frame_2... 上传给穗禾上游。
//
// 仅识别 http(s) 协议；data URI 与本地文件不在此处处理（multipart 文件路径已覆盖）。
func collectImageURLs(req relaycommon.TaskSubmitReq) []string {
	out := make([]string, 0, len(req.Images)+1)
	seen := make(map[string]struct{}, len(req.Images)+1)
	add := func(u string) {
		u = strings.TrimSpace(u)
		if u == "" {
			return
		}
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			return
		}
		if _, dup := seen[u]; dup {
			return
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	add(req.InputReference)
	for _, u := range req.Images {
		add(u)
	}
	add(req.Image)
	return out
}

// writeRemoteImageAsPart 拉取远程图片，按 multipart part 名称写入到上游请求体。
//
// 设计权衡：
//   - 直接走默认 http.Client（无代理），与图床服务（七牛云 OSS 等）走公网最直接；
//   - 加 30s 超时，避免恶意/慢速图床拖死整个上游链路；
//   - Content-Type 优先取响应头，否则走前 512 字节嗅探；
//   - filename 取 URL 末段，没有时回退到 fieldName + 推导后缀。
func writeRemoteImageAsPart(writer *multipart.Writer, fieldName, imageURL string) error {
	httpReq, err := http.NewRequest(http.MethodGet, imageURL, nil)
	if err != nil {
		return errors.Wrap(err, "new_http_request_failed")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return errors.Wrap(err, "fetch_image_failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("fetch image returned http %d", resp.StatusCode)
	}

	// 读出首段用于 Content-Type 嗅探（响应头未给出时），再拼回完整字节。
	const sniffLen = 512
	head := make([]byte, sniffLen)
	n, _ := io.ReadFull(resp.Body, head)
	head = head[:n]
	rest, err := io.ReadAll(resp.Body)
	if err != nil {
		return errors.Wrap(err, "read_image_body_failed")
	}
	body := append(head, rest...)

	ct := resp.Header.Get("Content-Type")
	if ct == "" || strings.HasPrefix(ct, "application/octet-stream") {
		ct = http.DetectContentType(head)
	}

	filename := guessFilenameFromURL(imageURL, fieldName, ct)

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, fieldName, filename))
	h.Set("Content-Type", ct)
	part, err := writer.CreatePart(h)
	if err != nil {
		return errors.Wrap(err, "create_multipart_part_failed")
	}
	if _, err := part.Write(body); err != nil {
		return errors.Wrap(err, "copy_image_to_part_failed")
	}
	return nil
}

// guessFilenameFromURL 从 URL 末段提取文件名；缺失时按 Content-Type 推导后缀。
func guessFilenameFromURL(rawURL, fieldName, contentType string) string {
	tail := rawURL
	if idx := strings.LastIndex(tail, "?"); idx >= 0 {
		tail = tail[:idx]
	}
	if idx := strings.LastIndex(tail, "/"); idx >= 0 {
		tail = tail[idx+1:]
	}
	if tail != "" && strings.Contains(tail, ".") {
		return tail
	}
	ext := ".bin"
	switch {
	case strings.Contains(contentType, "png"):
		ext = ".png"
	case strings.Contains(contentType, "jpeg"), strings.Contains(contentType, "jpg"):
		ext = ".jpg"
	case strings.Contains(contentType, "webp"):
		ext = ".webp"
	case strings.Contains(contentType, "gif"):
		ext = ".gif"
	}
	return fieldName + ext
}

// writeMultipartFile 把上传文件原样写入新的 multipart writer（保留 filename + Content-Type）。
func writeMultipartFile(writer *multipart.Writer, fieldName string, fh *multipart.FileHeader) error {
	f, err := fh.Open()
	if err != nil {
		return err
	}
	defer f.Close()

	ct := fh.Header.Get("Content-Type")
	if ct == "" || ct == "application/octet-stream" {
		sniff := make([]byte, 512)
		n, _ := io.ReadFull(f, sniff)
		ct = http.DetectContentType(sniff[:n])
		// 嗅探后重新打开，确保完整内容被复制。
		f.Close()
		f, err = fh.Open()
		if err != nil {
			return err
		}
		defer f.Close()
	}

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, fieldName, fh.Filename))
	h.Set("Content-Type", ct)
	part, err := writer.CreatePart(h)
	if err != nil {
		return err
	}
	_, err = io.Copy(part, f)
	return err
}

// mapOpenAISizeToSuihe 把 OpenAI 视频 API 的 size 字段（"WxH"，如 "720x1280"、"1280x720"、
// "1920x1080" 等）拆解为 suihe 文档要求的 video_resolution + ratio。
//
// 解析规则：
//   - 以最大边判分辨率：≤ 720 → 720p，≤ 1080 → 1080p，更大走 1080p（按文档推断）。
//   - 比例使用最小公约数化简：1280:720 → 16:9，720:1280 → 9:16，1024:1024 → 1:1。
//
// 解析失败时（空值或非法格式）返回空串，让上游沿用其默认值。
func mapOpenAISizeToSuihe(size string) (resolution, ratio string) {
	parts := strings.Split(strings.TrimSpace(size), "x")
	if len(parts) != 2 {
		return "", ""
	}
	w, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
	if w <= 0 || h <= 0 {
		return "", ""
	}
	maxDim := w
	if h > w {
		maxDim = h
	}
	switch {
	case maxDim <= 720:
		resolution = "720p"
	default:
		resolution = "1080p"
	}
	g := gcd(w, h)
	if g > 0 {
		ratio = fmt.Sprintf("%d:%d", w/g, h/g)
	}
	return
}

func gcd(a, b int) int {
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	resp, err := channel.DoTaskApiRequest(a, c, info, requestBody)
	// 穗禾上游对异步任务提交按规范返回 202 Accepted；framework 仅以 200 视为成功，
	// 这里把 2xx 统一收敛为 200，确保 DoResponse 能解析 task_id 而不是被包成 fail_to_fetch_task。
	if err == nil && resp != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		resp.StatusCode = http.StatusOK
	}
	return resp, err
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
	case "480p":
		return 0.3
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
