package digest

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ValidateConfig validates the JSON stored in a digest config's `config`
// column. It returns an error when the payload is not valid JSON or when a
// field violates its constraints (min_score must be >= 0; zero means "no
// threshold"). The HTTP layer calls this on create/update so bad configs are
// rejected at the API boundary instead of failing later at generation time.
func ValidateConfig(raw string) error {
	if raw == "" {
		return nil
	}
	var cfg digestConfigJSON
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return fmt.Errorf("config must be valid JSON: %w", err)
	}
	if cfg.MinScore < 0 {
		return fmt.Errorf("min_score must be >= 0, got %d", cfg.MinScore)
	}
	if cfg.SendEmail {
		if !isPlausibleEmail(cfg.EmailTo) {
			return fmt.Errorf("send_email requires a valid email_to address, got %q", cfg.EmailTo)
		}
	}
	return nil
}

// isPlausibleEmail performs a lightweight sanity check: exactly one "@", with
// non-empty local part and domain containing at least one dot.
func isPlausibleEmail(s string) bool {
	at := strings.Index(s, "@")
	if at <= 0 || at != strings.LastIndex(s, "@") || at == len(s)-1 {
		return false
	}
	domain := s[at+1:]
	dot := strings.LastIndex(domain, ".")
	return dot > 0 && dot < len(domain)-1 && !strings.ContainsAny(s, " 	\r\n")
}
