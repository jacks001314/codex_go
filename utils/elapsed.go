package utils

import (
	"fmt"
	"time"
)

func FormatDuration(duration time.Duration) string {
	return FormatElapsedMillis(duration.Milliseconds())
}

func FormatElapsedMillis(millis int64) string {
	if millis < 1000 {
		return fmt.Sprintf("%dms", millis)
	}
	if millis < 60000 {
		return fmt.Sprintf("%.2fs", float64(millis)/1000.0)
	}
	minutes := millis / 60000
	seconds := (millis % 60000) / 1000
	return fmt.Sprintf("%dm %02ds", minutes, seconds)
}
