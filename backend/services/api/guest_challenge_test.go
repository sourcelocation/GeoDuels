package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestRateLimitSubjectGroupsIPv6By64(t *testing.T) {
	for ip, want := range map[string]string{
		"203.0.113.7":                   "203.0.113.7",
		"::ffff:203.0.113.7":            "203.0.113.7",
		"2001:db8:1:2:aaaa::1":          "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff:ffff:ffff:1": "2001:db8:1:2::/64",
		"":                              "unknown",
	} {
		if got := rateLimitSubject(ip); got != want {
			t.Errorf("rateLimitSubject(%q) = %q, want %q", ip, got, want)
		}
	}
}

func TestGuestChallengeFromEnv(t *testing.T) {
	t.Setenv("TURNSTILE_SITE_KEY", "")
	t.Setenv("TURNSTILE_SECRET_KEY", "")
	none, err := guestChallengeFromEnv(http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	if err := none.verify(context.Background(), "token", ""); !errors.Is(err, errGuestChallengeFailed) {
		t.Fatalf("no challenge verified a token: %v", err)
	}
	t.Setenv("TURNSTILE_SITE_KEY", "site")
	if _, err := guestChallengeFromEnv(http.DefaultClient); err == nil {
		t.Fatal("a site key without a secret was accepted")
	}
}

func TestGuestChallengeRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		challenge guestChallenge
		status    int
	}{
		"none":      {noGuestChallenge{}, http.StatusTooManyRequests},
		"turnstile": {turnstileChallenge{siteKey: "site"}, http.StatusPreconditionRequired},
	} {
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/api/auth/guest", nil), rec)
		if err := tc.challenge.refuse(c, 0); err != nil {
			t.Fatal(err)
		}
		if rec.Code != tc.status {
			t.Errorf("%s refused with %d, want %d", name, rec.Code, tc.status)
		}
	}
}

func TestTurnstileChallengeVerify(t *testing.T) {
	verdict := `{"success":true,"action":"guest_signup","hostname":"geoduels.io"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("secret") != "secret" || r.FormValue("response") != "token" || r.FormValue("remoteip") != "203.0.113.7" {
			http.Error(w, "unexpected form", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(verdict))
	}))
	defer server.Close()
	challenge := turnstileChallenge{client: server.Client(), siteKey: "site", secret: "secret", verifyURL: server.URL, hostname: "geoduels.io"}
	ctx := context.Background()

	if err := challenge.verify(ctx, "token", "203.0.113.7"); err != nil {
		t.Fatalf("a valid token failed: %v", err)
	}
	if err := challenge.verify(ctx, " ", "203.0.113.7"); !errors.Is(err, errGuestChallengeFailed) {
		t.Fatalf("an empty token passed: %v", err)
	}
	for _, rejected := range []string{
		`{"success":false}`,
		`{"success":true,"action":"login","hostname":"geoduels.io"}`,
		`{"success":true,"action":"guest_signup","hostname":"evil.example"}`,
	} {
		verdict = rejected
		if err := challenge.verify(ctx, "token", "203.0.113.7"); !errors.Is(err, errGuestChallengeFailed) {
			t.Errorf("verdict %s passed: %v", rejected, err)
		}
	}
	if err := challenge.verify(ctx, "other", "203.0.113.7"); err == nil || errors.Is(err, errGuestChallengeFailed) {
		t.Fatalf("an unavailable verifier gave a verdict: %v", err)
	}
}
