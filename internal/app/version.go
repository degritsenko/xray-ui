package app

import (
	"context"
	"errors"
	"os/exec"
	"regexp"
	"time"
)

var coreVersionPattern = regexp.MustCompile(`^Xray ([0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?) `)

// DetectXrayVersion reads the installed core once, without exposing its raw output.
func DetectXrayVersion(ctx context.Context, binary string) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	data, err := exec.CommandContext(probeCtx, binary, "version").Output()
	if err != nil {
		return "", errors.New("could not read Xray version")
	}
	match := coreVersionPattern.FindSubmatch(data)
	if len(match) != 2 {
		return "", errors.New("unrecognized Xray version")
	}
	return string(match[1]), nil
}
func (m *Manager) SetCoreVersion(version string) { m.mu.Lock(); m.coreVersion = version; m.mu.Unlock() }
