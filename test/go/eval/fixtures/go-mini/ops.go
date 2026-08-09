package evalmini

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func Add(a, b int) int { return 0 }

func Clamp(value, low, high int) int { return value }

func NormalizeSpace(value string) string { return value }

func Deduplicate(values []string) []string { return values }

func ParsePort(value string) (int, error) { return 0, nil }

func Slug(value string) string { return value }

func RetryDelay(attempt int) time.Duration { return 0 }

func Redact(value, secret string) string { return value }

func Chunk(values []string, size int) [][]string { return nil }

func IsLocalURL(raw string) bool {
	_, _ = url.Parse(raw)
	return false
}

var _ = fmt.Sprintf
var _ = regexp.MustCompile
var _ = strconv.Atoi
var _ = strings.TrimSpace
