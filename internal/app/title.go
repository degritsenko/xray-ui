package app

import (
	"context"
	"encoding/base64"
	"strings"
)

// SubscriptionDownloader extends Runtime without requiring metadata from every downloader.
type SubscriptionDownloader interface {
	DownloadSubscription(context.Context, string, string) ([]byte, string, error)
}

func decodeProfileTitle(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 1024 {
		return ""
	}
	if strings.HasPrefix(value, "base64:") {
		encoded := strings.TrimPrefix(value, "base64:")
		valid := false
		for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			if decoded, err := encoding.DecodeString(encoded); err == nil {
				value = strings.TrimSpace(string(decoded))
				valid = true
				break
			}
		}
		if !valid {
			return ""
		}
	}
	if value == "" || len(value) > 160 || !validText(value) {
		return ""
	}
	return value
}
func subscriptionTitle(header string, body []byte) string {
	if title := decodeProfileTitle(header); title != "" {
		return title
	}
	return decodeProfileTitle(subscriptionBodyHeader(body, "profile-title"))
}
func subscriptionBodyHeader(body []byte, header string) string {
	text := strings.TrimSpace(strings.TrimPrefix(string(body), "\ufeff"))
	if !strings.Contains(text, "://") {
		encoded := strings.Join(strings.Fields(text), "")
		for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			if decoded, err := encoding.DecodeString(encoded); err == nil {
				text = string(decoded)
				break
			}
		}
	}
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "#"+header) {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
