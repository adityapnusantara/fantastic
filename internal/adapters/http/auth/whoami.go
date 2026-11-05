package auth

import (
	"github.com/labstack/echo/v5"

	"github.com/adityapratamanusantara/fantastic/internal/adapters/http/middleware"
	"github.com/adityapratamanusantara/fantastic/internal/adapters/http/response"
)

// Whoami reports the verified identity of the caller. Protected by the
// bearer-auth middleware; useful as a connectivity check for clients.
//
//	@Summary	Who am I
//	@Tags		auth
//	@Success	200	{object}	response.Envelope
//	@Failure	401	{object}	response.Envelope
//	@Security	bearerAuth
//	@Router		/whoami [get]
func Whoami(c *echo.Context) error {
	p, ok := middleware.PrincipalFrom(c)
	if !ok {
		return response.Fail(c, 500, "internal", "missing principal")
	}
	return response.OK(c, map[string]string{
		"user_id":      p.UserID,
		"organization": p.Organization,
	})
}
