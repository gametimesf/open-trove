package main

import (
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"strings"

	"github.com/labstack/echo/v4"
)

const (
	troveUserEmailHeader = "X-Trove-User-Email"
	troveUserEmailCookie = "trove_user_email"

	userEmailContextKey       = "trove_user_email"
	userEmailSourceContextKey = "trove_user_email_source"
)

// requestIdentityMiddleware resolves audit-only user attribution on every
// request. API and agent clients provide X-Trove-User-Email directly; browser
// pages cache the same manually entered email in a host-scoped cookie and add
// the header to fetch requests. The email is never used for authorization.
func requestIdentityMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			email, source := resolveRequestUserEmail(c.Request())
			if email != "" {
				c.Set(userEmailContextKey, email)
				c.Set(userEmailSourceContextKey, source)

				// Give handlers and mounted integrations one canonical
				// identity header regardless of its external source.
				c.Request().Header.Set(troveUserEmailHeader, email)
			}

			log.Printf(
				"INFO  request identity method=%s path=%q user_email=%q source=%s",
				c.Request().Method,
				c.Request().URL.Path,
				email,
				source,
			)

			if isWriteMethod(c.Request().Method) && email == "" {
				return c.JSON(http.StatusBadRequest, map[string]string{
					"error": fmt.Sprintf(
						"user email is required for write requests; send %s",
						troveUserEmailHeader,
					),
				})
			}

			return next(c)
		}
	}
}

func resolveRequestUserEmail(r *http.Request) (string, string) {
	if email := normalizeEmail(r.Header.Get(troveUserEmailHeader)); email != "" {
		return email, "header"
	}

	if cookie, err := r.Cookie(troveUserEmailCookie); err == nil {
		if email := normalizeEmail(cookie.Value); email != "" {
			return email, "cookie"
		}
	}

	return "", "missing"
}

func normalizeEmail(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 254 {
		return ""
	}

	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value {
		return ""
	}
	at := strings.LastIndexByte(value, '@')
	if at <= 0 || at == len(value)-1 || !strings.Contains(value[at+1:], ".") {
		return ""
	}
	return strings.ToLower(value)
}

func isWriteMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func userEmail(c echo.Context) string {
	email, _ := c.Get(userEmailContextKey).(string)
	return email
}
