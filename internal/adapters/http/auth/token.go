// Package auth implements the dev-only token endpoint that exchanges a
// username and password for a Casdoor access token, keeping the Casdoor
// client secret server-side.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/adityapratamanusantara/fantastic/config"
	"github.com/adityapratamanusantara/fantastic/internal/adapters/http/response"
)

// loginRequest is the demo page's login payload.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// tokenResponse carries the issued access token.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
}

// Client exchanges credentials for Casdoor access tokens.
type Client struct {
	cfg        *config.Config
	secret     func() (string, error)
	httpClient *http.Client
}

// NewClient builds a Casdoor login client using the resolved client
// secret function.
func NewClient(cfg *config.Config, secret func() (string, error)) *Client {
	return &Client{
		cfg:        cfg,
		secret:     secret,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// Register mounts the dev token endpoint when enabled in configuration;
// when disabled, nothing is mounted so requests route to 404 (the spec's
// "disabled means 404" behaviour).
func Register(e *echo.Echo, cfg *config.Config) error {
	if !cfg.Auth.DevTokenEnabled {
		return nil
	}
	c := NewClient(cfg, cfg.ClientSecret)
	e.POST("/api/v1/auth/token", c.Token)
	return nil
}

// Token handles POST /api/v1/auth/token.
//
//	@Summary		Exchange credentials for a token (dev only)
//	@Description	Password grant proxy to Casdoor; disabled unless auth.dev_token_endpoint is true (then the route is absent and returns 404).
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			request	body	loginRequest	true	"username and password"
//	@Success		200	{object}	response.Envelope
//	@Failure		401	{object}	response.Envelope
//	@Router			/auth/token [post]
func (cl *Client) Token(c *echo.Context) error {
	var req loginRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, "invalid_request", "username and password required")
	}
	if req.Username == "" || req.Password == "" {
		return response.Fail(c, http.StatusBadRequest, "invalid_request", "username and password required")
	}

	token, err := cl.Exchange(c.Request().Context(), req.Username, req.Password)
	if err != nil {
		if errors.Is(err, errInvalidCredentials) {
			return response.Fail(c, http.StatusUnauthorized, "unauthorized", "invalid credentials")
		}
		return response.Fail(c, http.StatusBadGateway, "upstream_error", "casdoor login failed")
	}

	return response.OK(c, tokenResponse{AccessToken: token})
}

var errInvalidCredentials = errors.New("invalid credentials")

// Exchange performs the OAuth2 password grant against the Casdoor token
// endpoint, resolving the user within the configured application's
// organization.
func (cl *Client) Exchange(ctx context.Context, username, password string) (string, error) {
	secret, err := cl.secret()
	if err != nil {
		return "", fmt.Errorf("resolve client secret: %w", err)
	}

	endpoint := strings.TrimRight(cl.cfg.Auth.Issuer, "/") + "/api/login/oauth/access_token"

	form := url.Values{
		"grant_type":    {"password"},
		"client_id":     {cl.cfg.Auth.ClientID},
		"client_secret": {secret},
		"username":      {username},
		"password":      {password},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := cl.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("call casdoor: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if out.AccessToken == "" {
		if out.Error != "" {
			return "", errInvalidCredentials
		}
		return "", fmt.Errorf("casdoor returned no token")
	}
	return out.AccessToken, nil
}
