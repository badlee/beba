package auth

import (
	"github.com/gofiber/fiber/v3"
)

// Server handles Beba acting as an OAuth2 Provider.
type Server struct {
	Manager *Manager
}

func RegisterProviderRoutes(router fiber.Router, m *Manager) {
	s := &Server{Manager: m}
	group := router.Group("/oauth2")

	group.Get("/authorize", s.handleAuthorize)
	group.Post("/token", s.handleToken)
	group.Get("/userinfo", s.handleUserInfo)
}

func (s *Server) handleAuthorize(c fiber.Ctx) error {
	// 1. Validate client_id and redirect_uri
	// 2. Show login/authorization page
	// 3. Generate auth code
	return c.SendString("Authorize Page")
}

func (s *Server) handleToken(c fiber.Ctx) error {
	// 1. Validate auth code or refresh token
	// 2. Issue access token
	return c.JSON(fiber.Map{
		"access_token": "mock-access-token",
		"token_type":   "Bearer",
		"expires_in":   3600,
	})
}

func (s *Server) handleUserInfo(c fiber.Ctx) error {
	// 1. Validate access token
	// 2. Return user profile
	return c.JSON(fiber.Map{
		"sub":      "user-id",
		"username": "user",
	})
}
