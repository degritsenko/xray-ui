package app

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSubscriptionUsageParsingAndOptionalFields(t *testing.T) {
	u := subscriptionUsage("upload=100; download=200; total=1000; expire=1798761600", nil)
	if u == nil || *u.Upload != 100 || *u.Download != 200 || *u.Total != 1000 || *u.Expire != 1798761600 {
		t.Fatalf("usage not parsed: %+v", u)
	}
	u = subscriptionUsage("total=0; expire=0", nil)
	if u == nil || u.Upload != nil || u.Download != nil || *u.Total != 0 || *u.Expire != 0 {
		t.Fatal("unlimited/no expiry conflated with missing data")
	}
	for _, raw := range []string{"", "upload=-1; total=NaN; expire=invalid", "total=18446744073709551615", "expire=253402300800", "download=9007199254740992", "unknown=42"} {
		if subscriptionUsage(raw, nil) != nil {
			t.Fatalf("invalid metadata accepted: %s", raw)
		}
	}
	body := []byte("#subscription-userinfo: download=512; expire=0\n" + sampleURI)
	encoded := []byte(base64.StdEncoding.EncodeToString(body))
	for _, b := range [][]byte{body, encoded} {
		u = subscriptionUsage("", b)
		if u == nil || *u.Download != 512 || *u.Expire != 0 {
			t.Fatal("body metadata missing")
		}
	}
	u = subscriptionUsage("download=42", body)
	if *u.Download != 42 {
		t.Fatal("body replaced header")
	}
}
func TestDownloadCapturesUsageAndFailedRefreshRetainsMetadata(t *testing.T) {
	invalid := false
	header := "upload=100; download=200; total=1000; expire=1798761600"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Subscription-Userinfo", header)
		w.Header().Set("Profile-Title", "Usage VPN")
		if invalid {
			_, _ = w.Write([]byte("invalid response"))
		} else {
			_, _ = w.Write([]byte(sampleURI))
		}
	}))
	defer server.Close()
	m, _, _, id := testManager(t)
	m.network = &LinuxRuntime{iface: "unused", client: server.Client()}
	state := m.stateCopy()
	state.Subscriptions[0].URL = server.URL
	if err := m.save(state); err != nil {
		t.Fatal(err)
	}
	if err := m.Refresh(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	usage := m.Snapshot().Subscriptions[0].Usage
	if usage == nil || *usage.Download != 200 {
		t.Fatal("response header not exposed in snapshot")
	}
	invalid = true
	header = "download=999"
	if err := m.Refresh(context.Background(), id); err == nil {
		t.Fatal("bad subscription accepted")
	}
	if *m.Snapshot().Subscriptions[0].Usage.Download != 200 {
		t.Fatal("failed refresh replaced accepted metadata")
	}
	invalid = false
	header = ""
	if err := m.Refresh(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().Subscriptions[0].Usage != nil {
		t.Fatal("missing header retained stale metadata")
	}
}
