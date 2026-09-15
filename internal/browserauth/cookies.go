package browserauth

import (
	"fmt"
	"sort"
	"strings"
)

// These are the cookie sources used by libgm's Google Account login flow.
var cookieDomains = map[string]string{
	"SID": "google.com", "HSID": "google.com", "SSID": "google.com",
	"APISID": "google.com", "SAPISID": "google.com",
	"OSID": "messages.google.com", "__Secure-1PSIDTS": "google.com",
}

// FilterCookies retains only Google Messages credentials and checks that all
// required cookies are present. Errors contain cookie names, never values.
func FilterCookies(input map[string]string) (map[string]string, error) {
	filtered := make(map[string]string, len(cookieDomains))
	var missing []string
	for name := range cookieDomains {
		if value := input[name]; strings.TrimSpace(value) != "" {
			filtered[name] = value
		} else if name != "__Secure-1PSIDTS" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("cookie input is missing required names: %s", strings.Join(missing, ", "))
	}
	return filtered, nil
}
