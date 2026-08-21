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
		if err := validateEmailTo(cfg.EmailTo); err != nil {
			return err
		}
	}
	return nil
}

// validateEmailTo performs a light sanity check on the delivery address:
// exactly one @ with non-empty local and domain parts.
func validateEmailTo(addr string) error {
	parts := strings.Split(addr, "@")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("email_to must be a valid email address, got %q", addr)
	}
	return nil
}
