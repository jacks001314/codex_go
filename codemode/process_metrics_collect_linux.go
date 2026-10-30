//go:build linux

package codemode

import (
	"os"
	"strconv"
	"strings"
)

// defaultProcessMetricsCollector observes the host process from /proc so the
// observation stays read-only and bounded (Rust #57841).
func defaultProcessMetricsCollector() (ProcessMetrics, error) {
	data, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return ProcessMetrics{}, err
	}
	stat := string(data)
	closeParen := strings.LastIndexByte(stat, ')')
	if closeParen < 0 || closeParen+2 >= len(stat) {
		return ProcessMetrics{}, errInvalidProcStat
	}
	fields := strings.Fields(stat[closeParen+2:])
	// fields[0] is the third stat column (state); starttime is column 22,
	// utime 14, stime 15, rss 24.
	if len(fields) < 22 {
		return ProcessMetrics{}, errInvalidProcStat
	}
	parse := func(index int) (uint64, error) { return strconv.ParseUint(fields[index], 10, 64) }
	utime, err := parse(11)
	if err != nil {
		return ProcessMetrics{}, errInvalidProcStat
	}
	stime, err := parse(12)
	if err != nil {
		return ProcessMetrics{}, errInvalidProcStat
	}
	startTicks, err := parse(19)
	if err != nil {
		return ProcessMetrics{}, errInvalidProcStat
	}
	rssPages, err := parse(21)
	if err != nil {
		return ProcessMetrics{}, errInvalidProcStat
	}
	boot, err := procBootTimeSeconds()
	if err != nil {
		return ProcessMetrics{}, err
	}
	clkTck := float64(100)
	pageSize := float64(os.Getpagesize())
	return ProcessMetrics{
		ProcessID:               uint32(os.Getpid()),
		ProcessStartTimeSeconds: boot + float64(startTicks)/clkTck,
		CPUSecondsTotal:         float64(utime+stime) / clkTck,
		ResidentMemoryBytes:     uint64(float64(rssPages) * pageSize),
	}, nil
}

func procBootTimeSeconds() (float64, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "btime ") {
			continue
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(line, "btime ")), 64)
		if err != nil {
			return 0, errInvalidProcStat
		}
		return value, nil
	}
	return 0, errInvalidProcStat
}
