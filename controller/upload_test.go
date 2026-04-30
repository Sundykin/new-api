package controller

import (
	"testing"
)

func TestBuildQiniuTimestampAntiLeechURL(t *testing.T) {
	// 使用七牛官方文档示例可直接参与的自构造用例：
	// 对 path /test/1.jpg，key=foo，deadline 由函数自行计算，校验输出格式。
	raw := "https://cdn.example.com/test/1.jpg"
	signed := buildQiniuTimestampAntiLeechURL(raw, "foo", 3600)

	if !contains(signed, "sign=") || !contains(signed, "t=") {
		t.Fatalf("signed url missing sign/t: %s", signed)
	}
	if !startsWith(signed, "https://cdn.example.com/test/1.jpg?") {
		t.Fatalf("signed url prefix wrong: %s", signed)
	}

	// 含中文路径：应走 EscapedPath，避免直接拼中文导致签名异常
	raw2 := "https://cdn.example.com/图片/a b.png"
	signed2 := buildQiniuTimestampAntiLeechURL(raw2, "foo", 60)
	if !contains(signed2, "sign=") {
		t.Fatalf("signed2 missing sign: %s", signed2)
	}
}

func contains(s, sub string) bool {
	return indexOf(s, sub) >= 0
}

func startsWith(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestBuildQiniuUploadToken(t *testing.T) {
	ak := "test_access_key"
	sk := "test_secret_key"
	bucket := "test-bucket"
	key := "test/image.jpg"
	expireSeconds := int64(3600)

	token := buildQiniuUploadToken(ak, sk, bucket, key, expireSeconds)

	if token == "" {
		t.Fatal("token should not be empty")
	}
	if len(token) < 50 {
		t.Fatalf("token too short: %s", token)
	}
	// 格式: AccessKey:Sign:EncodedPutPolicy
	parts := splitN(token, ":", 3)
	if len(parts) != 3 {
		t.Fatalf("token format invalid, expected 3 parts, got %d: %s", len(parts), token)
	}
	if parts[0] != ak {
		t.Fatalf("first part should be access key, got %s", parts[0])
	}
}

func splitN(s, sep string, n int) []string {
	var result []string
	for i := 0; i < n-1; i++ {
		idx := indexByte(s, sep[0])
		if idx < 0 {
			break
		}
		result = append(result, s[:idx])
		s = s[idx+1:]
	}
	result = append(result, s)
	return result
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
