// Package duration — сроки, которые пишут люди: Go duration (30m, 2h) и дни (1d, 7d).
package duration

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mechta-market/pulse_agent/internal/errs"
)

// Parse — срок: 30m, 2h, 1d, 7d; пусто — 0 (без срока). Ошибки: errs.InvalidRequest.
func Parse(v string) (time.Duration, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, nil
	}
	if days, ok := strings.CutSuffix(v, "d"); ok {
		if n, err := strconv.Atoi(days); err == nil && n > 0 {
			return time.Duration(n) * 24 * time.Hour, nil
		}
	} else if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d, nil
	}
	return 0, fmt.Errorf("%w: duration %q; expected 30m, 2h, 1d, 7d or empty", errs.InvalidRequest, v)
}
