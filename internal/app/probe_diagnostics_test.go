package app

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestProbeRetriesTransientFailureOnce(t *testing.T) {
	calls := 0
	delay, err := retryProbe(context.Background(), func(context.Context) (time.Duration, error) {
		calls++
		if calls == 1 {
			return 0, &probeFailure{message: "TLS handshake timed out", retryable: true}
		}
		return 42 * time.Millisecond, nil
	})
	if err != nil || calls != 2 || delay != 42*time.Millisecond {
		t.Fatalf("retry did not recover: %d %v %v", calls, delay, err)
	}
	calls = 0
	_, err = retryProbe(context.Background(), func(context.Context) (time.Duration, error) {
		calls++
		return 0, &probeFailure{message: "Invalid probe URL"}
	})
	if err == nil || calls != 1 {
		t.Fatal("permanent failure retried")
	}
	calls = 0
	_, err = retryProbe(context.Background(), func(context.Context) (time.Duration, error) {
		calls++
		return 0, &probeFailure{message: "VPN connection timed out", retryable: true}
	})
	if err == nil || calls != 2 {
		t.Fatal("retry count is not bounded")
	}
}
func TestProbeRetryStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	_, err := retryProbe(ctx, func(context.Context) (time.Duration, error) {
		calls++
		cancel()
		return 0, &probeFailure{message: "VPN connection failed", retryable: true}
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("cancelled probe retried", calls, err)
	}
}
func TestFailedProbeRetainsLastSuccessfulMeasurementAndSafeReason(t *testing.T) {
	m, _, _, id := testManager(t)
	ctx := context.Background()
	if err := m.Refresh(ctx, id); err != nil {
		t.Fatal(err)
	}
	p := &fakeProber{}
	m.SetProber(p)
	node := m.stateCopy().Subscriptions[0].Nodes[0]
	if !m.checkNode(ctx, id, node) {
		t.Fatal("initial probe failed")
	}
	first := m.Snapshot().Subscriptions[0].Nodes[0].Probe
	p.failed = map[string]bool{node.ID: true}
	if m.checkNode(ctx, id, node) {
		t.Fatal("failed probe succeeded")
	}
	failed := m.Snapshot().Subscriptions[0].Nodes[0].Probe
	if failed.Reachable || failed.LastSuccess == nil || failed.LastSuccess.Milliseconds != 42 || !failed.LastSuccess.CheckedAt.Equal(first.CheckedAt) || failed.Error == "" {
		t.Fatalf("lost last success: %+v", failed)
	}
	if strings.Contains(safeProbeError(errors.New("secret https://provider.example/token")), "secret") {
		t.Fatal("raw error leaked into UI")
	}
	p.failed = nil
	m.checkNode(ctx, id, node)
	recovered := m.Snapshot().Subscriptions[0].Nodes[0].Probe
	if !recovered.Reachable || recovered.Error != "" {
		t.Fatal("recovered probe retains failure")
	}
}

func TestPermanentCertificateAndHTTPFailuresAreNotRetried(t *testing.T) {
	for _, err := range []error{proxyFailure(x509.UnknownAuthorityError{}, 1), httpProbeFailure(403), httpProbeFailure(302)} {
		calls := 0
		_, got := retryProbe(context.Background(), func(context.Context) (time.Duration, error) { calls++; return 0, err })
		if got == nil || calls != 1 {
			t.Fatal("permanent test-site failure retried", calls, got)
		}
	}
	if safeProbeError(httpProbeFailure(503)) != "Test site returned HTTP 503" {
		t.Fatal("HTTP reason missing")
	}
}

func TestDNSRetryPolicyDoesNotRetryMissingRecords(t *testing.T) {
	permanent := &net.DNSError{Err: "no such host", Name: "private.example", IsNotFound: true}
	calls := 0
	_, err := retryProbe(context.Background(), func(context.Context) (time.Duration, error) { calls++; return 0, dnsProbeFailure(permanent, false) })
	if err == nil || calls != 1 || strings.Contains(safeProbeError(err), "private.example") {
		t.Fatal("permanent DNS error retried or leaked details", calls, err)
	}
	transient := &net.DNSError{Err: "temporary failure", IsTemporary: true}
	failure := dnsProbeFailure(transient, false).(*probeFailure)
	if !failure.retryable {
		t.Fatal("temporary DNS failure is not retryable")
	}
}
