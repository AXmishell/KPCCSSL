package certificate

import (
	"testing"
	"time"
)

func TestRenewalTimeUsesLifetime(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	nowUnix := now.Unix()
	cases := []struct {
		name      string
		notBefore int64
		notAfter  int64
		want      int64
	}{
		{"90d/30d-window", nowUnix, nowUnix + 90*86400, nowUnix + 60*86400},
		{"47d/third", nowUnix, nowUnix + 47*86400, nowUnix + 47*86400 - (47*86400)/3},
		{"2h clamp", nowUnix, nowUnix + 2*3600, nowUnix + 3600},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RenewalTime(c.notBefore, c.notAfter, now)
			if got != c.want {
				t.Fatalf("got %d want %d", got, c.want)
			}
		})
	}
}
