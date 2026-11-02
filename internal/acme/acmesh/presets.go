package acmesh

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// CAPreset is an ACME directory advertised by acme.sh.
type CAPreset struct {
	ID           string
	Name         string
	DirectoryURL string
	NeedsEAB     bool
}

// assignmentRE captures `NAME="value"`, `NAME='value'` and `NAME=value`.
// Quoted values may span lines (CA_NAMES is a multi-line block).
var assignmentRE = regexp.MustCompile(`(?ms)^([A-Za-z_][A-Za-z0-9_]*)=(?:"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)'|([^\s#]+))`)

var variableRE = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// presetMetadata pins stable IDs and friendly display names for known CAs.
// Unknown entries added upstream still flow through with their acme.sh name.
var presetMetadata = map[string]CAPreset{
	"https://acme-v02.api.letsencrypt.org/directory": {ID: "letsencrypt", Name: "Let's Encrypt"},
	"https://acme.zerossl.com/v2/DV90":               {ID: "zerossl", Name: "ZeroSSL", NeedsEAB: true},
	"https://acme.ssl.com/sslcom-dv-rsa":             {ID: "sslcom", Name: "SSL.com (RSA)", NeedsEAB: true},
	"https://acme.ssl.com/sslcom-dv-ecc":             {ID: "sslcom-ecc", Name: "SSL.com (ECC)", NeedsEAB: true},
	"https://dv.acme-v02.api.pki.goog/directory":     {ID: "google", Name: "Google Trust Services", NeedsEAB: true},
	"https://acme-api.actalis.com/acme/directory":    {ID: "actalis", Name: "Actalis", NeedsEAB: true},
}

// DiscoverCAPresets reads the production CA list from an acme.sh script.
// acme.sh has no machine-readable command for this, so the hard-coded
// CA_NAMES/CA_SERVERS variables are parsed; callers should keep a built-in
// fallback. Staging/test directories are skipped.
func DiscoverCAPresets(bin string) ([]CAPreset, error) {
	data, err := os.ReadFile(bin)
	if err != nil {
		return nil, err
	}
	variables := parseAssignments(string(data))

	namesRaw, ok := variables["CA_NAMES"]
	if !ok {
		return nil, fmt.Errorf("%s: CA_NAMES not found", bin)
	}
	serversRaw, ok := variables["CA_SERVERS"]
	if !ok {
		return nil, fmt.Errorf("%s: CA_SERVERS not found", bin)
	}

	names := parseCANames(namesRaw)
	servers := splitList(expandVariables(serversRaw, variables, 0))
	if len(names) == 0 || len(names) != len(servers) {
		return nil, fmt.Errorf("%s: CA_NAMES has %d entries, CA_SERVERS has %d", bin, len(names), len(servers))
	}

	presets := make([]CAPreset, 0, len(names)+1)
	seen := map[string]bool{}
	usedURLs := map[string]bool{}

	add := func(preset CAPreset) {
		if preset.DirectoryURL == "" || usedURLs[preset.DirectoryURL] {
			return
		}
		usedURLs[preset.DirectoryURL] = true
		id := preset.ID
		if id == "" {
			id = slug(preset.Name)
		}
		for seen[id] {
			id += "-2"
		}
		seen[id] = true
		preset.ID = id
		presets = append(presets, preset)
	}

	for index, name := range names {
		directory := strings.TrimSpace(servers[index])
		if !strings.HasPrefix(directory, "http://") && !strings.HasPrefix(directory, "https://") {
			return nil, fmt.Errorf("%s: invalid CA_SERVERS entry %q for %q", bin, directory, name.display)
		}
		if isTestDirectory(name.display, directory) {
			continue
		}
		add(newPreset(name.display, name.aliases, directory))
		// acme.sh switches SSL.com between the RSA and ECC directories based
		// on key type; expose the ECC directory as a separate preset.
		if directory == "https://acme.ssl.com/sslcom-dv-rsa" {
			if ecc := strings.TrimSpace(expandVariables(variables["CA_SSLCOM_ECC"], variables, 0)); ecc != "" {
				add(newPreset("SSL.com (ECC)", []string{"sslcom-ecc"}, ecc))
			}
		}
	}
	return presets, nil
}

type caName struct {
	display string
	aliases []string
}

func parseCANames(raw string) []caName {
	var names []caName
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, ",")
		name := caName{display: strings.TrimSpace(parts[0])}
		for _, alias := range parts[1:] {
			if alias = strings.TrimSpace(alias); alias != "" {
				name.aliases = append(name.aliases, alias)
			}
		}
		if name.display != "" {
			names = append(names, name)
		}
	}
	return names
}

func newPreset(display string, aliases []string, directory string) CAPreset {
	preset := CAPreset{
		ID:           slug(firstNonEmpty(aliases)),
		Name:         display,
		DirectoryURL: directory,
		NeedsEAB:     needsEAB(directory),
	}
	if meta, ok := presetMetadata[strings.TrimRight(directory, "/")]; ok {
		preset.ID = meta.ID
		preset.Name = meta.Name
		preset.NeedsEAB = preset.NeedsEAB || meta.NeedsEAB
	}
	return preset
}

func parseAssignments(script string) map[string]string {
	out := map[string]string{}
	for _, match := range assignmentRE.FindAllStringSubmatch(script, -1) {
		value := match[2]
		if value == "" {
			value = match[3]
		}
		if value == "" {
			value = match[4]
		}
		value = strings.ReplaceAll(value, `\"`, `"`)
		value = strings.ReplaceAll(value, `\'`, `'`)
		out[match[1]] = value
	}
	return out
}

func expandVariables(value string, variables map[string]string, depth int) string {
	if depth > 8 || !strings.Contains(value, "$") {
		return value
	}
	return variableRE.ReplaceAllStringFunc(value, func(token string) string {
		name := strings.TrimPrefix(strings.TrimSuffix(strings.TrimPrefix(token, "${"), "}"), "$")
		replacement, ok := variables[name]
		if !ok {
			return token
		}
		return expandVariables(replacement, variables, depth+1)
	})
}

func splitList(raw string) []string {
	var out []string
	for _, entry := range strings.Split(raw, ",") {
		if entry = strings.TrimSpace(entry); entry != "" {
			out = append(out, entry)
		}
	}
	return out
}

func isTestDirectory(name, directory string) bool {
	haystack := strings.ToLower(name + " " + directory)
	for _, marker := range []string{"staging", "_test", "test-api", "test.api", "test-acme"} {
		if strings.Contains(haystack, marker) {
			return true
		}
	}
	return false
}

func needsEAB(directory string) bool {
	host := strings.ToLower(directory)
	for _, marker := range []string{"zerossl.com", "pki.goog", "ssl.com", "actalis.com"} {
		if strings.Contains(host, marker) {
			return true
		}
	}
	return false
}

func slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var out strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out.WriteRune(r)
		case r == '.' || r == '_' || r == '-' || r == ' ':
			out.WriteRune('-')
		}
	}
	return strings.Trim(out.String(), "-")
}

func firstNonEmpty(values []string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
