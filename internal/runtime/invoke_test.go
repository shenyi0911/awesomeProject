package runtime

import "testing"

func TestValidDaprMethod(t *testing.T) {
	valid := []string{"calculate", "predict.v2", "predict_v2", "Predict-2"}
	for _, m := range valid {
		if !validDaprMethod(m) {
			t.Fatalf("validDaprMethod(%q) = false, want true", m)
		}
	}
	invalid := []string{"", "../escape", "a/b", "a b", "a?b", "a#b"}
	for _, m := range invalid {
		if validDaprMethod(m) {
			t.Fatalf("validDaprMethod(%q) = true, want false", m)
		}
	}
}

func TestProxyMethod(t *testing.T) {
	cases := map[string]string{
		"/calculate":  "calculate",
		"/predict.v2": "predict.v2",
		"/healthz":    "",
		"/dapr":       "",
		"/../escape":  "other",
		"":            "",
	}
	for path, want := range cases {
		if got := proxyMethod(path); got != want {
			t.Fatalf("proxyMethod(%q) = %q, want %q", path, got, want)
		}
	}
}
