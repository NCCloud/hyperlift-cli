package apps

import (
	"strconv"
	"time"
)

// The nullable Application fields are pointers. Each helper below renders a nil,
// which is a wire null, as "-".

func dash(s *string) string {
	if s == nil || *s == "" {
		return "-"
	}

	return *s
}

// fmtScale renders the scale, 0 or 1; null means it is not yet known.
func fmtScale(v *int) string {
	if v == nil {
		return "-"
	}

	return strconv.Itoa(*v)
}

func fmtInstallID(id *int64) string {
	if id == nil {
		return "-"
	}

	return strconv.FormatInt(*id, 10)
}

func fmtBool(v *bool) string {
	if v == nil {
		return "-"
	}

	return strconv.FormatBool(*v)
}

func fmtTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}

	return t.Format(time.RFC3339)
}
