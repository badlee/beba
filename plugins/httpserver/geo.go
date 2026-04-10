package httpserver

import (
	"net"
	"slices"

	"github.com/gofiber/fiber/v3"
	"github.com/oschwald/geoip2-golang"
)

// GeoMiddleware blocks or allows requests based on the client's country.
func GeoMiddleware(cfg *GeoConfig, db *geoip2.Reader) fiber.Handler {
	return func(c fiber.Ctx) error {
		if db == nil || cfg == nil || !cfg.Enabled {
			return c.Next()
		}

		clientIP := net.ParseIP(c.IP())
		if clientIP == nil {
			return c.Next()
		}

		record, err := db.Country(clientIP)
		if err != nil {
			// If we can't find the country, we allow by default
			return c.Next()
		}

		countryCode := record.Country.IsoCode

		// 1. Check blocklist
		if len(cfg.BlockCountries) > 0 {
			if slices.Contains(cfg.BlockCountries, countryCode) {
				return c.Status(fiber.StatusForbidden).SendString("Access denied from your country (" + countryCode + ")")
			}
		}

		// 2. Check allowlist
		if len(cfg.AllowCountries) > 0 {
			if !slices.Contains(cfg.AllowCountries, countryCode) {
				return c.Status(fiber.StatusForbidden).SendString("Access denied from your country (" + countryCode + ")")
			}
		}

		return c.Next()
	}
}
