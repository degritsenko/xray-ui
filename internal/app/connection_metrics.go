package app

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type ConnectionMetrics struct {
	StartedAt        time.Time `json:"startedAt"`
	TrafficStartedAt time.Time `json:"trafficStartedAt"`
	SampledAt        time.Time `json:"sampledAt"`
	DownloadBytes    uint64    `json:"downloadBytes"`
	UploadBytes      uint64    `json:"uploadBytes"`
	Available        bool      `json:"available"`
}

func connectionMetrics(started, trafficStarted time.Time) *ConnectionMetrics {
	m := &ConnectionMetrics{StartedAt: started, TrafficStartedAt: trafficStarted}
	read := func(name string) (uint64, error) {
		data, err := os.ReadFile("/sys/class/net/tun0/statistics/" + name)
		if err != nil {
			return 0, err
		}
		return strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	}
	// RX is traffic received by the kernel from Xray; TX is sent to Xray.
	down, downErr := read("rx_bytes")
	up, upErr := read("tx_bytes")
	m.SampledAt = time.Now().UTC()
	m.Available = downErr == nil && upErr == nil
	if m.Available {
		m.DownloadBytes, m.UploadBytes = down, up
	}
	return m
}
