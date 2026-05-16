package shortcache

import (
	"time"
)

const (
	NullValue     = "__NULL__"
	MappingPrefix = "shorturl:surl:"
	BloomKey      = "shorturl:bloom:surl"
)

func MappingKey(shortCode string) string {
	return MappingPrefix + shortCode
}

func MappingTTL(seconds int) time.Duration {
	if seconds <= 0 {
		seconds = 24 * 60 * 60
	}

	jitter := time.Duration(time.Now().UnixNano()%600) * time.Second
	return time.Duration(seconds)*time.Second + jitter
}

func NullTTL() time.Duration {
	return time.Minute
}
