package strm

import (
	"encoding/base64"
)

const emptyPathKey = "-"

func EncodeFileKey(fileID string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fileID))
}

func DecodeFileKey(fileKey string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(fileKey)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// EncodePathKey 将目录 ID 或相对路径编码为单个安全的 URL 路径段。
// "-" 专门表示空目录 ID，兼容以空字符串表示根目录的驱动。
func EncodePathKey(value string) string {
	if value == "" {
		return emptyPathKey
	}
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func DecodePathKey(key string) (string, error) {
	if key == emptyPathKey {
		return "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(key)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
