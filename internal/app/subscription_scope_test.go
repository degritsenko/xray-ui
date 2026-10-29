package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type partialSubscriptionRuntime struct{ *testRuntime }

func (r *partialSubscriptionRuntime) Download(ctx context.Context, u, ua string) ([]byte, error) {
	if strings.Contains(u, "offline.example") {
		return nil, errors.New("could not download subscription")
	}
	return r.testRuntime.Download(ctx, u, ua)
}
func TestFailedSecondarySubscriptionDoesNotSetGlobalConnectionError(t *testing.T) {
	m, r, c, _ := testManager(t)
	s := m.stateCopy()
	second := s.Subscriptions[0]
	second.ID = "offline"
	second.URL = "https://offline.example/sub"
	s.Subscriptions = append(s.Subscriptions, second)
	if err := m.save(s); err != nil {
		t.Fatal(err)
	}
	m.network = &partialSubscriptionRuntime{r}
	m.SetProber(&fakeProber{})
	if err := m.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	view := m.Snapshot()
	if !c.Running() || view.LastError != "" || view.Subscriptions[1].Error == "" {
		t.Fatalf("subscription failure duplicated globally: running=%v global=%q local=%q", c.Running(), view.LastError, view.Subscriptions[1].Error)
	}
}
func TestRefreshResponseIdentifiesSubscriptionScopedError(t *testing.T) {
	s, m := setupHTTP(t)
	m.network.(*testRuntime).err = errors.New("could not download subscription")
	cookie, csrf := login(t, s)
	id := m.stateCopy().Subscriptions[0].ID
	r := request(t, s.Client(), "POST", s.URL+"/api/subscriptions/"+id+"/refresh", `{}`, cookie, csrf, s.URL)
	defer r.Body.Close()
	var body map[string]string
	if json.NewDecoder(r.Body).Decode(&body) != nil || r.StatusCode != 400 || body["scope"] != "subscription" {
		t.Fatalf("scoped failure not identified: %d %v", r.StatusCode, body)
	}
}
