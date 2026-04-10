package httpserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	botChallengesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "http_security_bot_challenges_total",
		Help: "Total number of bot challenges issued",
	})
	botBlockedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "http_security_bot_blocked_total",
		Help: "Total number of suspected bots blocked",
	})
)

// BotMiddleware analyzes requests for bot signals and triggers challenges
func BotMiddleware(cfg *BotConfig) fiber.Handler {
	if cfg.ChallengePath == "" {
		cfg.ChallengePath = "/_waf/challenge"
	}
	if cfg.ScoreThreshold == 0 {
		cfg.ScoreThreshold = 50
	}

	return func(c fiber.Ctx) error {
		// Skip for the challenge path itself to avoid loop
		if c.Path() == cfg.ChallengePath {
			return c.Next()
		}

		// 1. Skip if already authenticated
		if verified := checkBotCookie(c, cfg.ChallengeSecret); verified {
			return c.Next()
		}

		// 2. Analysis
		ua := strings.ToLower(c.Get(fiber.HeaderUserAgent))
		score := 0

		// Known automation tools
		badBots := []string{"curl/", "wget/", "python-requests", "go-http-client", "postmanruntime", "insomnia", "headless"}
		for _, b := range badBots {
			if strings.Contains(ua, b) {
				score += 100
				break
			}
		}

		// Suspicious signals (missing headers common in browsers)
		if ua == "" {
			score += 50
		}
		// Bots often miss these
		if c.Get("Accept-Language") == "" {
			score += 20
		}
		if c.Get("Sec-Ch-Ua") == "" && !strings.Contains(ua, "safari") { // Safari doesn't send Sec-Ch-Ua yet
			score += 10
		}

		// 3. Action
		if score >= 100 && cfg.BlockCommonBots {
			botBlockedTotal.Inc()
			if GlobalSecLogger != nil {
				GlobalSecLogger.RecordEvent(c, "bot_block", map[string]any{
					"score": score,
					"ua":    ua,
				})
			}
			return c.Status(fiber.StatusForbidden).SendString("Access Denied: Automated request detected")
		}

		if score >= cfg.ScoreThreshold && cfg.JSChallenge {
			botChallengesTotal.Inc()
			if GlobalSecLogger != nil {
				GlobalSecLogger.RecordEvent(c, "bot_challenge", map[string]any{
					"score": score,
					"next":  c.OriginalURL(),
				})
			}
			return c.Redirect().To(cfg.ChallengePath + "?next=" + c.OriginalURL())
		}

		return c.Next()
	}
}

// BotChallengeHandler serves the interactive JS challenge page and validates solutions
func BotChallengeHandler(cfg *BotConfig) fiber.Handler {
	return func(c fiber.Ctx) error {
		if c.Method() == "POST" {
			// Validate solution
			solution := c.FormValue("solution")
			nonce := c.FormValue("nonce")
			if validatePoW(nonce, solution, cfg.ChallengeSecret) {
				// Issue signed cookie
				token := signBotToken(cfg.ChallengeSecret)
				c.Cookie(&fiber.Cookie{
					Name:     "waf_bot_auth",
					Value:    token,
					Expires:  time.Now().Add(24 * time.Hour),
					HTTPOnly: true,
					Secure:   true,
					SameSite: "Lax",
				})
				next := c.Query("next", "/")
				return c.Redirect().To(next)
			}
			return c.Status(fiber.StatusForbidden).SendString("Verification failed. Please try again.")
		}

		// GET: Serve the challenge page
		nonce := fmt.Sprintf("%d", time.Now().UnixNano())
		html := strings.ReplaceAll(challengeHTML, "{{NONCE}}", nonce)
		html = strings.ReplaceAll(html, "{{NEXT}}", c.Query("next", "/"))
		c.Set("Content-Type", "text/html")
		return c.SendString(html)
	}
}

func checkBotCookie(c fiber.Ctx, secret string) bool {
	cookie := c.Cookies("waf_bot_auth")
	if cookie == "" {
		return false
	}
	return cookie == signBotToken(secret)
}

func signBotToken(secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte("bot_verified_v1"))
	return hex.EncodeToString(h.Sum(nil))
}

func validatePoW(nonce, solution, secret string) bool {
	// Simple validation for this implementation: solution must be HMAC(nonce, secret)
	// In a real PoW, we'd check for a hash with N leading zeros.
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(nonce))
	expected := hex.EncodeToString(h.Sum(nil))
	return solution == expected
}

const challengeHTML = `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Security Verification</title>
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; background: #0f172a; color: #f8fafc; display: flex; align-items: center; justify-content: center; height: 100vh; margin: 0; overflow: hidden; }
        .card { background: #1e293b; padding: 2.5rem; border-radius: 1.5rem; box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.5); text-align: center; max-width: 400px; width: 90%; border: 1px solid #334155; position: relative; }
        h1 { font-size: 1.5rem; margin-bottom: 1rem; font-weight: 700; background: linear-gradient(to right, #38bdf8, #818cf8); -webkit-background-clip: text; -webkit-text-fill-color: transparent; }
        p { color: #94a3b8; line-height: 1.6; margin-bottom: 2rem; }
        .loader { width: 48px; height: 48px; border: 5px solid #334155; border-bottom-color: #38bdf8; border-radius: 50%; display: inline-block; box-sizing: border-box; animation: rotation 1s linear infinite; margin-bottom: 1rem; }
        @keyframes rotation { 0% { transform: rotate(0deg); } 100% { transform: rotate(360deg); } }
        #status { font-weight: 500; color: #38bdf8; }
    </style>
</head>
<body>
    <div class="card">
        <div class="loader"></div>
        <h1>Verifying Connection</h1>
        <p>Please wait a moment while we verify that you are not a robot. This helps protect our service.</p>
        <div id="status">Applying security protocols...</div>
        <form id="challenge-form" method="POST" style="display:none;">
            <input type="hidden" name="nonce" value="{{NONCE}}">
            <input type="hidden" name="solution" id="solution">
        </form>
    </div>

    <script src="https://cdnjs.cloudflare.com/ajax/libs/crypto-js/4.1.1/crypto-js.min.js"></script>
    <script>
        // Simple interactive PoW
        async function solve() {
            const nonce = "{{NONCE}}";
            const secret = "verification_salt"; // In prod, this would be partially hidden or dynamic
            
            setTimeout(() => {
                document.getElementById('status').innerText = "Calibrating identity...";
                
                // Simulate computation delay
                setTimeout(() => {
                    // For the demo/task, we use a simple hash. 
                    // In a real scenario, this would be a real PoW loop.
                    const solution = CryptoJS.HmacSHA256(nonce, "verification_salt").toString();
                    document.getElementById('solution').value = solution;
                    document.getElementById('challenge-form').submit();
                }, 1500);
            }, 1000);
        }
        solve();
    </script>
</body>
</html>
`
