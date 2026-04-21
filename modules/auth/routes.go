package auth

import (
	"github.com/gofiber/fiber/v3"
)

func RegisterRoutes(router fiber.Router, m *Manager) {
	group := router.Group("/auth")

	group.Post("/login", func(c fiber.Ctx) error {
		var creds struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Strategy string `json:"strategy"`
		}
		if err := c.Bind().JSON(&creds); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
		}

		user, err := m.Authenticate(c.Context(), creds.Strategy, map[string]string{
			"username": creds.Username,
			"password": creds.Password,
		})
		if err != nil {
			return c.Status(401).JSON(fiber.Map{"error": err.Error()})
		}

		// Generate token (JWT)
		// ...
		return c.JSON(fiber.Map{"user": user, "token": "mock-token"})
	})

	group.Get("/me", func(c fiber.Ctx) error {
		// Verify token
		// ...
		return c.JSON(fiber.Map{"username": "mock-user"})
	})

	// OAuth2 Client routes
	group.Get("/login/:strategy", func(c fiber.Ctx) error {
		strategyName := c.Params("strategy")
		m.mu.RLock()
		defer m.mu.RUnlock()

		for _, s := range m.strategies {
			if s.Name() == strategyName {
				if oauth, ok := s.(*OAuth2Strategy); ok {
					return c.Redirect().To(oauth.AuthURL("state"))
				}
			}
		}
		return c.Status(404).SendString("Strategy not found")
	})

	group.Get("/callback/:strategy", func(c fiber.Ctx) error {
		strategyName := c.Params("strategy")
		code := c.Query("code")
		user, err := m.Authenticate(c.Context(), strategyName, map[string]string{"code": code})
		if err != nil {
			return c.Status(401).SendString(err.Error())
		}
		return c.JSON(user)
	})

	// OAuth2 Provider routes
	RegisterProviderRoutes(router, m)
}
