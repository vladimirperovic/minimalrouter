package storage

import (
	"errors"
	"fmt"
)

const (
	WarningUsagePercent  = 80.0
	CriticalUsagePercent = 90.0
)

type PressureLevel string

const (
	PressureUnknown  PressureLevel = "unknown"
	PressureNormal   PressureLevel = "normal"
	PressureWarning  PressureLevel = "warning"
	PressureCritical PressureLevel = "critical"
)

var ErrCriticalPressure = errors.New("durable writes blocked by critical disk pressure")

type Status struct {
	Available                 bool          `json:"available"`
	TotalBytes                uint64        `json:"total_bytes,omitempty"`
	UsedBytes                 uint64        `json:"used_bytes,omitempty"`
	FreeBytes                 uint64        `json:"free_bytes,omitempty"`
	UsagePercent              float64       `json:"usage_percent,omitempty"`
	Level                     PressureLevel `json:"level"`
	NonessentialWritesAllowed bool          `json:"nonessential_writes_allowed"`
	DurableWritesAllowed      bool          `json:"durable_writes_allowed"`
}

func Evaluate(totalBytes, availableBytes uint64) Status {
	status := Status{
		Available:                 totalBytes > 0,
		TotalBytes:                totalBytes,
		FreeBytes:                 availableBytes,
		Level:                     PressureUnknown,
		NonessentialWritesAllowed: true,
		DurableWritesAllowed:      true,
	}
	if totalBytes == 0 {
		return status
	}
	if availableBytes > totalBytes {
		availableBytes = totalBytes
		status.FreeBytes = availableBytes
	}
	status.UsedBytes = totalBytes - availableBytes
	status.UsagePercent = float64(status.UsedBytes) / float64(totalBytes) * 100
	switch {
	case status.UsagePercent >= CriticalUsagePercent:
		status.Level = PressureCritical
		status.NonessentialWritesAllowed = false
		status.DurableWritesAllowed = false
	case status.UsagePercent >= WarningUsagePercent:
		status.Level = PressureWarning
	default:
		status.Level = PressureNormal
	}
	return status
}

func RequireDurableWrite(path string) error {
	status := Inspect(path)
	if status.Available && !status.DurableWritesAllowed {
		return fmt.Errorf("%w: %.1f%% used", ErrCriticalPressure, status.UsagePercent)
	}
	return nil
}

func AllowNonessentialWrite(path string) bool {
	status := Inspect(path)
	return !status.Available || status.NonessentialWritesAllowed
}
