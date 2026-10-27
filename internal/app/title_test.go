package app

import (
	"context"
	"encoding/base64"
	"testing"
)

func TestSubscriptionTitleFormats(t *testing.T) {
	for _, tc := range []struct{ header, body, want string }{
		{"cloud.example.com", sampleURI, "cloud.example.com"},
		{"base64:" + base64.StdEncoding.EncodeToString([]byte("My VPN 🌍")), sampleURI, "My VPN 🌍"},
		{"", "#profile-title: lin.example.com\n" + sampleURI, "lin.example.com"},
		{"", base64.StdEncoding.EncodeToString([]byte("#profile-title: base64:TXkgVlBO\n" + sampleURI)), "My VPN"},
		{"Header", "#profile-title: Body\n" + sampleURI, "Header"},
		{"base64:!!!!", sampleURI, ""}, {"bad\x00title", sampleURI, ""},
	} {
		if got := subscriptionTitle(tc.header, []byte(tc.body)); got != tc.want {
			t.Fatalf("got %q want %q", got, tc.want)
		}
	}
}

type titleRuntime struct {
	*testRuntime
	title string
}

func (r *titleRuntime) DownloadSubscription(ctx context.Context, u, ua string) ([]byte, string, error) {
	b, e := r.Download(ctx, u, ua)
	return b, r.title, e
}
func TestProviderTitlePersistsAndFailedRefreshRetainsIt(t *testing.T) {
	m, r, _, id := testManager(t)
	network := &titleRuntime{testRuntime: r, title: "Cloud VPN"}
	m.network = network
	if err := m.Refresh(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().Subscriptions[0].Name != "Cloud VPN" {
		t.Fatal("provider title missing")
	}
	r.body = "invalid response"
	network.title = "Wrong title"
	if err := m.Refresh(context.Background(), id); err == nil {
		t.Fatal("invalid subscription accepted")
	}
	if m.Snapshot().Subscriptions[0].Name != "Cloud VPN" {
		t.Fatal("failed refresh replaced title")
	}
	stored, err := m.store.(*Store).LoadOrImport(func(k string) string {
		if k == "SUBSCRIPTION_URL" {
			return "https://example.com/sub"
		}
		return ""
	})
	if err != nil || stored.Subscriptions[0].Title != "Cloud VPN" {
		t.Fatal("restart lost cached title", err)
	}
}
func TestSubscriptionNameFallsBackToHostname(t *testing.T) {
	m, _, _, _ := testManager(t)
	if got := m.Snapshot().Subscriptions[0].Name; got != "example.com" {
		t.Fatal("hostname fallback missing", got)
	}
}
