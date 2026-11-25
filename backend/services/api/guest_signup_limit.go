package main

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"geoduels/pkg/ratelimit"
)

const guestSignupRateLimitKeyPrefix = "api:ratelimit:guest_signup:ip:"

// hitGuestSignupLimit counts a guest signup against its address's window. Past the limit a signup
// still goes through once it passes the guest challenge.
func (a *api) hitGuestSignupLimit(ctx context.Context, ip string) (bool, time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	return ratelimit.Hit(ctx, a.db.Pool(), guestSignupRateLimitKeyPrefix+rateLimitSubject(ip), a.guestSignupIPLimit, a.guestSignupIPWindow)
}

func writeRateLimited(c echo.Context, retryAfter time.Duration) error {
	if retryAfter > 0 {
		seconds := max(1, int(retryAfter.Round(time.Second).Seconds()))
		c.Response().Header().Set("Retry-After", strconv.Itoa(seconds))
	}
	return plainTextError(c, http.StatusTooManyRequests, "too many guest signups")
}
