package division

import "github.com/gofiber/fiber/v2"

func RegisterRoutes(app fiber.Router) {
	app.Post("/test", clawtest)
}
