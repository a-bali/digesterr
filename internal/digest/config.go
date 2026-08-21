package digest

import (
	"encoding/json"
	"fmt"
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
	return nil
}
