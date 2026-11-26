package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

// guestChallenge is what a guest signup past its address's limit has to pass.
type guestChallenge interface {
	// verify is nil when the token proves a person and errGuestChallengeFailed when it doesn't; any
	// other error means there is no verdict.
	verify(ctx context.Context, token, ip string) error
	// refuse answers a signup past the limit that hasn't passed.
	refuse(c echo.Context, retryAfter time.Duration) error
}

var errGuestChallengeFailed = errors.New("guest challenge failed")

// guestChallengeFromEnv is Turnstile when both of its keys are set, and otherwise none at all.
func guestChallengeFromEnv(client *http.Client) (guestChallenge, error) {
	siteKey := strings.TrimSpace(os.Getenv("TURNSTILE_SITE_KEY"))
	secret := strings.TrimSpace(os.Getenv("TURNSTILE_SECRET_KEY"))
	if siteKey == "" && secret == "" {
		return noGuestChallenge{}, nil
	}
	if siteKey == "" || secret == "" {
		return nil, errors.New("TURNSTILE_SITE_KEY and TURNSTILE_SECRET_KEY are set together")
	}
	return turnstileChallenge{
		client:    client,
		siteKey:   siteKey,
		secret:    secret,
		verifyURL: getenv("TURNSTILE_VERIFY_URL", turnstileSiteverifyURL),
		hostname:  strings.TrimSpace(os.Getenv("TURNSTILE_EXPECTED_HOSTNAME")),
	}, nil
}

// noGuestChallenge leaves nothing to pass: past the limit, signups wait for the window to end.
type noGuestChallenge struct{}

func (noGuestChallenge) verify(context.Context, string, string) error {
	return errGuestChallengeFailed
}

func (noGuestChallenge) refuse(c echo.Context, retryAfter time.Duration) error {
	return writeRateLimited(c, retryAfter)
}

const turnstileSiteverifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"
const guestTurnstileAction = "guest_signup"

type turnstileChallenge struct {
	client    *http.Client
	siteKey   string
	secret    string
	verifyURL string
	hostname  string
}

// turnstileRequired tells the browser to show the widget and send its token with the next attempt.
type turnstileRequired struct {
	Error            string `json:"error"`
	TurnstileSiteKey string `json:"turnstileSiteKey"`
}

func (t turnstileChallenge) refuse(c echo.Context, _ time.Duration) error {
	return writeJSONStatus(c, http.StatusPreconditionRequired, turnstileRequired{Error: "verification_required", TurnstileSiteKey: t.siteKey})
}

type turnstileSiteverifyResponse struct {
	Success  bool   `json:"success"`
	Hostname string `json:"hostname"`
	Action   string `json:"action"`
}

func (t turnstileChallenge) verify(ctx context.Context, token, ip string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errGuestChallengeFailed
	}
	values := url.Values{}
	values.Set("secret", t.secret)
	values.Set("response", token)
	if ip != "" {
		values.Set("remoteip", ip)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.verifyURL, bytes.NewBufferString(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("turnstile verification unavailable")
	}
	var result turnstileSiteverifyResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}
	if !result.Success || (result.Action != "" && result.Action != guestTurnstileAction) ||
		(t.hostname != "" && !strings.EqualFold(result.Hostname, t.hostname)) {
		return errGuestChallengeFailed
	}
	return nil
}
