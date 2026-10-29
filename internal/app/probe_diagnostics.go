package app

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"time"
)

const (
	probeDNSTimeout     = 5 * time.Second
	probeStartupTimeout = 8 * time.Second
	probeRequestTimeout = 12 * time.Second
	probeTLSTimeout     = 8 * time.Second
)

type probeFailure struct {
	message   string
	retryable bool
}

func (e *probeFailure) Error() string { return e.message }
func safeProbeError(err error) string {
	if err == nil {
		return ""
	}
	var failure *probeFailure
	if errors.As(err, &failure) {
		return failure.message
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "Check timed out"
	}
	return "Check failed"
}
func retryProbe(ctx context.Context, attempt func(context.Context) (time.Duration, error)) (time.Duration, error) {
	for index := 0; index < 2; index++ {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		delay, err := attempt(ctx)
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		if err == nil {
			return delay, nil
		}
		var failure *probeFailure
		if index == 1 || !errors.As(err, &failure) || !failure.retryable {
			return 0, err
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, ctx.Err()
		case <-timer.C:
		}
	}
	panic("unreachable")
}
func proxyFailure(err error, phase int32) error {
	var unknown x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	if errors.As(err, &unknown) || errors.As(err, &hostname) || errors.As(err, &invalid) {
		return &probeFailure{message: "Test site certificate rejected"}
	}
	timedOut := errors.Is(err, context.DeadlineExceeded)
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		timedOut = true
	}
	name := "VPN connection"
	if phase == 1 {
		name = "TLS handshake"
	} else if phase >= 2 {
		name = "HTTPS response"
	}
	if timedOut {
		return &probeFailure{message: name + " timed out", retryable: true}
	}
	return &probeFailure{message: name + " failed", retryable: true}
}
func httpProbeFailure(status int) error {
	return &probeFailure{message: fmt.Sprintf("Test site returned HTTP %d", status), retryable: status == 408 || status == 425 || status == 429 || status >= 500}
}

func dnsProbeFailure(err error, timedOut bool) error {
	var dns *net.DNSError
	if timedOut || errors.Is(err, context.DeadlineExceeded) {
		return &probeFailure{message: "Server DNS lookup timed out", retryable: true}
	}
	if errors.As(err, &dns) {
		if dns.IsNotFound {
			return &probeFailure{message: "Server IPv4 DNS record not found"}
		}
		if dns.IsTimeout {
			return &probeFailure{message: "Server DNS lookup timed out", retryable: true}
		}
		return &probeFailure{message: "Server DNS lookup failed", retryable: dns.IsTemporary}
	}
	var networkError net.Error
	return &probeFailure{message: "Server DNS lookup failed", retryable: errors.As(err, &networkError) && (networkError.Timeout() || networkError.Temporary())}
}
