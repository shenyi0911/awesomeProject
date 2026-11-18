package runtime

import (
	"strings"
	"testing"
)

func TestAlgorithmEnvExcludesSecrets(t *testing.T) {
	t.Setenv("PG_DSN", "postgres://secret")
	t.Setenv("S3_ACCESS_KEY", "access")
	t.Setenv("S3_SECRET_KEY", "secret")
	t.Setenv("API_TOKEN", "token")

	env := algorithmEnv(1234)
	joined := strings.ToUpper(strings.Join(env, "\n"))
	for _, key := range []string{"PG_DSN", "S3_ACCESS_KEY", "S3_SECRET_KEY", "API_TOKEN"} {
		if strings.Contains(joined, key) {
			t.Fatalf("algorithm environment leaked %s: %v", key, env)
		}
	}
	if !strings.Contains(joined, "APP_PORT=1234") {
		t.Fatalf("algorithm environment is missing APP_PORT: %v", env)
	}
}
