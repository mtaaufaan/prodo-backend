// Package response holds the standard JSON response envelope --
// docs/coding-conventions.md §7.2 / docs/API_CONTRACT.md §1.
package response

import "github.com/gofiber/fiber/v2"

// Success membungkus data dalam envelope {"data": ...}.
func Success(data any) fiber.Map {
	return fiber.Map{"data": data}
}

// FieldError adalah satu item validasi gagal untuk ValidationError.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Error membungkus error dalam envelope {"error": {code, message, details}}.
func Error(code, message string, details any) fiber.Map {
	return fiber.Map{"error": fiber.Map{
		"code":    code,
		"message": message,
		"details": details,
	}}
}
