// Package response renders the standard JSON envelope for non-streaming
// endpoints: {"code": "...", "data": ...} on success and
// {"code": "...", "message": "..."} on failure.
package response

import (
	"net/http"

	"github.com/labstack/echo/v5"
)

// CodeSuccess marks a successful response.
const CodeSuccess = "00"

// Envelope is the standard response body shape.
type Envelope struct {
	Code    string `json:"code"`
	Data    any    `json:"data,omitempty"`
	Message string `json:"message,omitempty"`
}

// OK sends a success envelope with the given payload.
func OK(c *echo.Context, data any) error {
	return c.JSON(http.StatusOK, Envelope{Code: CodeSuccess, Data: data})
}

// Created sends a success envelope with HTTP 201.
func Created(c *echo.Context, data any) error {
	return c.JSON(http.StatusCreated, Envelope{Code: CodeSuccess, Data: data})
}

// Fail sends an error envelope with the given HTTP status and code.
func Fail(c *echo.Context, status int, code, message string) error {
	return c.JSON(status, Envelope{Code: code, Message: message})
}
