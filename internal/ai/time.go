package ai

import "time"

// TimeToMillis converts time to Unix milliseconds.
func TimeToMillis(t time.Time) int64 {
	return t.UnixMilli()
}

// MillisToTime converts Unix milliseconds to time.
func MillisToTime(ms int64) time.Time {
	return time.UnixMilli(ms)
}
