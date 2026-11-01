package acmesh

import (
	"strings"

	"github.com/axmishell/kpccssl/internal/acme"
)

// ParseListRaw parses `acme.sh --list --listraw` output. The format is
// header-prefixed and pipe-separated:
//
//	Main_Domain|KeyLength|SAN|Profile|CA|Created|Renew
func ParseListRaw(output string) []acme.ListEntry {
	var out []acme.ListEntry
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		cols := strings.Split(line, "|")
		if len(cols) < 7 {
			continue
		}
		if strings.EqualFold(cols[0], "Main_Domain") {
			continue
		}
		e := acme.ListEntry{
			MainDomain: strings.TrimSpace(cols[0]),
			KeyLength:  strings.Trim(strings.TrimSpace(cols[1]), `"'`),
			Profile:    strings.TrimSpace(cols[3]),
			CA:         strings.Trim(strings.TrimSpace(cols[4]), `"'`),
			Created:    strings.TrimSpace(cols[5]),
			Renew:      strings.TrimSpace(cols[6]),
		}
		sanCol := strings.Trim(strings.TrimSpace(cols[2]), `"'`)
		if sanCol != "" && !strings.EqualFold(sanCol, "no") {
			for _, san := range strings.Split(sanCol, ",") {
				s := strings.TrimSpace(san)
				if s != "" {
					e.SAN = append(e.SAN, s)
				}
			}
		}
		out = append(out, e)
	}
	return out
}
