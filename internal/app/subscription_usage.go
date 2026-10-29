package app

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

// Provider-supplied byte counters and a Unix expiry. Nil differs from explicit zero.
type SubscriptionUsage struct {
	Upload   *int64 `json:"upload,omitempty"`
	Download *int64 `json:"download,omitempty"`
	Total    *int64 `json:"total,omitempty"`
	Expire   *int64 `json:"expire,omitempty"`
}
type SubscriptionMetadata struct {
	Title string
	Usage *SubscriptionUsage
}
type SubscriptionMetadataDownloader interface {
	DownloadSubscriptionMetadata(context.Context, string, string) ([]byte, SubscriptionMetadata, error)
}

func parseSubscriptionUsage(value string) *SubscriptionUsage {
	if len(value) > 4096 {
		return nil
	}
	usage := &SubscriptionUsage{}
	for _, part := range strings.Split(value, ";") {
		key, text, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
		if err != nil || n < 0 || n > 9007199254740991 {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "upload":
			usage.Upload = &n
		case "download":
			usage.Download = &n
		case "total":
			usage.Total = &n
		case "expire":
			if n <= 253402300799 {
				usage.Expire = &n
			}
		}
	}
	if usage.Upload == nil && usage.Download == nil && usage.Total == nil && usage.Expire == nil {
		return nil
	}
	return usage
}
func subscriptionUsage(header string, body []byte) *SubscriptionUsage {
	if usage := parseSubscriptionUsage(header); usage != nil {
		return usage
	}
	return parseSubscriptionUsage(subscriptionBodyHeader(body, "subscription-userinfo"))
}

type subscriptionRefreshError struct{ cause error }

func (e *subscriptionRefreshError) Error() string { return e.cause.Error() }
func (e *subscriptionRefreshError) Unwrap() error { return e.cause }
func isSubscriptionRefreshError(err error) bool {
	var scoped *subscriptionRefreshError
	return errors.As(err, &scoped)
}
