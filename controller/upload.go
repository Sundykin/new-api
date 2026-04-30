package controller

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// 允许的图片扩展名
var qiniuAllowedImageExt = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".gif":  true,
	".webp": true,
	".bmp":  true,
	".svg":  true,
	".heic": true,
	".ico":  true,
}

// UploadImageToQiniu 通用图片上传接口：
//   - 通过 TokenAuth 中间件完成 API Key 鉴权
//   - 不消耗用户额度，仅通过 model.RecordLog 记录使用日志
//   - 文件存储到七牛云 Kodo（OSS）
//
// 所需环境变量：
//
//	QINIU_ACCESS_KEY    七牛云 AccessKey
//	QINIU_SECRET_KEY    七牛云 SecretKey
//	QINIU_BUCKET        存储空间名称
//	QINIU_DOMAIN        访问域名（例如 https://cdn.example.com）
//	QINIU_UPLOAD_HOST   上传域名，默认 https://upload.qiniup.com
//	QINIU_KEY_PREFIX    对象 key 前缀，默认 new-api/images
//	QINIU_MAX_UPLOAD_SIZE_MB 单文件最大尺寸（MB），默认 20
func UploadImageToQiniu(c *gin.Context) {
	ak := common.GetEnvOrDefaultString("QINIU_ACCESS_KEY", "")
	sk := common.GetEnvOrDefaultString("QINIU_SECRET_KEY", "")
	bucket := common.GetEnvOrDefaultString("QINIU_BUCKET", "")
	domain := common.GetEnvOrDefaultString("QINIU_DOMAIN", "")
	uploadHost := common.GetEnvOrDefaultString("QINIU_UPLOAD_HOST", "https://upload.qiniup.com")
	keyPrefix := common.GetEnvOrDefaultString("QINIU_KEY_PREFIX", "new-api/images")

	if ak == "" || sk == "" || bucket == "" || domain == "" {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "七牛云未配置: 请设置 QINIU_ACCESS_KEY / QINIU_SECRET_KEY / QINIU_BUCKET / QINIU_DOMAIN",
		})
		return
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "未找到上传文件，表单字段名必须为 file: " + err.Error(),
		})
		return
	}
	if fileHeader.Size <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "上传文件为空"})
		return
	}

	maxMB := int64(common.GetEnvOrDefault("QINIU_MAX_UPLOAD_SIZE_MB", 20))
	maxSize := maxMB * 1024 * 1024
	if fileHeader.Size > maxSize {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": fmt.Sprintf("文件过大，最大允许 %d MB", maxMB),
		})
		return
	}

	ext := strings.ToLower(filepath.Ext(fileHeader.Filename))
	if !qiniuAllowedImageExt[ext] {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "不支持的图片格式: " + ext,
		})
		return
	}

	src, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	defer src.Close()

	userId := c.GetInt("id")
	key := fmt.Sprintf("%s/%s/%d_%s%s",
		strings.Trim(keyPrefix, "/"),
		time.Now().Format("20060102"),
		userId,
		uuid.New().String(),
		ext,
	)

	upToken := buildQiniuUploadToken(ak, sk, bucket, key, 3600)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("key", key); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := writer.WriteField("token", upToken); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	part, err := writer.CreateFormFile("file", fileHeader.Filename)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	if _, err := io.Copy(part, src); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := writer.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, uploadHost, body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "上传到七牛云失败: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		c.JSON(http.StatusBadGateway, gin.H{
			"success": false,
			"message": fmt.Sprintf("七牛云返回错误: status=%d, body=%s", resp.StatusCode, string(respBody)),
		})
		return
	}

	var qiniuResp struct {
		Key  string `json:"key"`
		Hash string `json:"hash"`
	}
	_ = common.Unmarshal(respBody, &qiniuResp)

	fileURL := strings.TrimRight(domain, "/") + "/" + key

	// 时间戳防盗链：若配置了 QINIU_TIMESTAMP_ANTILEECH_KEY，则生成带签名的访问 URL
	antileechKey := common.GetEnvOrDefaultString("QINIU_TIMESTAMP_ANTILEECH_KEY", "")
	if antileechKey != "" {
		expireSec := int64(common.GetEnvOrDefault("QINIU_TIMESTAMP_ANTILEECH_EXPIRE", 259200))
		fileURL = buildQiniuTimestampAntiLeechURL(fileURL, antileechKey, expireSec)
	}

	tokenId := c.GetInt("token_id")
	tokenName := c.GetString("token_name")
	content := fmt.Sprintf("七牛云图片上传成功 key=%s size=%d token_id=%d token_name=%s",
		key, fileHeader.Size, tokenId, tokenName)
	model.RecordLog(userId, model.LogTypeSystem, content)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"url":      fileURL,
			"key":      key,
			"hash":     qiniuResp.Hash,
			"size":     fileHeader.Size,
			"filename": fileHeader.Filename,
		},
	})
}

// buildQiniuUploadToken 根据七牛云规范生成上传凭证：
// UploadToken = AccessKey + ":" + UrlsafeBase64(HMAC-SHA1(SecretKey, EncodedPutPolicy)) + ":" + EncodedPutPolicy
func buildQiniuUploadToken(ak, sk, bucket, key string, expireSeconds int64) string {
	deadline := time.Now().Unix() + expireSeconds
	putPolicy := fmt.Sprintf(`{"scope":"%s:%s","deadline":%d,"insertOnly":1}`, bucket, key, deadline)
	encoded := base64.URLEncoding.EncodeToString([]byte(putPolicy))
	h := hmac.New(sha1.New, []byte(sk))
	h.Write([]byte(encoded))
	sign := base64.URLEncoding.EncodeToString(h.Sum(nil))
	return fmt.Sprintf("%s:%s:%s", ak, sign, encoded)
}

// buildQiniuTimestampAntiLeechURL 七牛云 CDN 时间戳防盗链签名：
//
// 算法：
//  1. path = URL 的路径部分（含前导 /），对路径中每段做 url-encode（保留 /），
//     与七牛 CDN 校验行为保持一致。
//  2. deadline = 十六进制表示的到期 Unix 时间戳（小写）。
//  3. sign = md5_hex( EncryptKey + path + deadline )。
//  4. 在原 URL 的 query 中追加 sign=<sign>&t=<deadline>。
//
// 参数：
//   - rawURL: 资源完整 URL（含 scheme + host + path，可带 query）。
//   - encryptKey: 在七牛云控制台 CDN 域名「时间戳防盗链」中配置的 EncryptKey。
//   - expireSeconds: 链接有效期（秒）。
func buildQiniuTimestampAntiLeechURL(rawURL, encryptKey string, expireSeconds int64) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	// u.EscapedPath() 返回已按 RFC3986 转义的路径（保留 /）
	path := u.EscapedPath()
	deadline := fmt.Sprintf("%x", time.Now().Unix()+expireSeconds)

	h := md5.New()
	h.Write([]byte(encryptKey))
	h.Write([]byte(path))
	h.Write([]byte(deadline))
	sign := hex.EncodeToString(h.Sum(nil))

	q := u.Query()
	q.Set("sign", sign)
	q.Set("t", deadline)
	u.RawQuery = q.Encode()
	return u.String()
}
