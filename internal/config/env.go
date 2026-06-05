// env.go validates that all required environment variables are present
// before the server starts. Fail fast — never silently use empty secrets.
package config

import (
	"fmt"
	"os"
	"strings"
)

// requiredEnvVars lists every environment variable B-Edge needs to operate.
var requiredEnvVars = []string{
	"DB_HOST",
	"DB_PORT",
	"DB_NAME",
	"DB_USER",
	"DB_PASSWORD",
	"JWT_SECRET",
	"JWT_REFRESH_SECRET",
	"CLIENT_URL",
}

// jwtMinLength is the minimum acceptable length for JWT secrets.
const jwtMinLength = 32

// ValidateEnv checks all required environment variables are set and
// that JWT secrets meet minimum length requirements.
func ValidateEnv() error {
	var missing []string

	for _, key := range requiredEnvVars {
		if os.Getenv(key) == "" {
			missing = append(missing, key)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	if len(os.Getenv("JWT_SECRET")) < jwtMinLength {
		return fmt.Errorf("JWT_SECRET must be at least %d characters", jwtMinLength)
	}

	if len(os.Getenv("JWT_REFRESH_SECRET")) < jwtMinLength {
		return fmt.Errorf("JWT_REFRESH_SECRET must be at least %d characters", jwtMinLength)
	}

	return nil
}
