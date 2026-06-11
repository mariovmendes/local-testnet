package observability

import "testing"

func TestSkipPrometheusEnabled(t *testing.T) {
	t.Setenv(skipPrometheusEnv, "true")
	if !skipPrometheus() {
		t.Fatal("expected Prometheus to be skipped when the env var is true")
	}
}

func TestSkipPrometheusDisabledByDefault(t *testing.T) {
	t.Setenv(skipPrometheusEnv, "")
	if skipPrometheus() {
		t.Fatal("expected Prometheus not to be skipped by default")
	}
}
