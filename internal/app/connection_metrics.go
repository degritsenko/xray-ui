package app

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type ConnectionMetrics struct {
	StartedAt     time.Time `json:"startedAt"`
	SampledAt     time.Time `json:"sampledAt"`
	DownloadBytes uint64    `json:"downloadBytes"`
	UploadBytes   uint64    `json:"uploadBytes"`
	Available     bool      `json:"available"`
}

func connectionMetrics(started time.Time) *ConnectionMetrics {
	m := &ConnectionMetrics{StartedAt: started}
	read := func(name string) (uint64, error) {
		data, err := os.ReadFile("/sys/class/net/tun0/statistics/" + name)
		if err != nil {
			return 0, err
		}
		return strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	}
	// TUN TX delivers downloaded traffic to the kernel; RX receives uploads.
	down, downErr := read("tx_bytes")
	up, upErr := read("rx_bytes")
	m.SampledAt = time.Now().UTC()
	m.Available = downErr == nil && upErr == nil
	if m.Available {
		m.DownloadBytes, m.UploadBytes = down, up
	}
	return m
}
