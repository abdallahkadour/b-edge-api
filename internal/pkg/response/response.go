// Package response provides standardised HTTP response helpers for the B-Edge API.
// All handlers use these helpers — never call c.JSON directly in a handler.
package response

import "github.com/gofiber/fiber/v2"

// Body is the standard success response envelope.
// Used only for Swagger documentation — never instantiated directly.
type Body struct {
	Data  interface{} `json:"data"`
	Error interface{} `json:"error"`
	Meta  *Meta       `json:"meta"`
}

// ErrorBody is the standard error response envelope.
// Used only for Swagger documentation — never instantiated directly.
type ErrorBody struct {
	Data  interface{}  `json:"data"`
	Error ErrorDetails `json:"error"`
	Meta  interface{}  `json:"meta"`
}

// ErrorDetails contains the machine-readable error code and human-readable message.
type ErrorDetails struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Meta holds pagination metadata for list responses.
// NextCursor is the keyset cursor for fetching the next page.
type Meta struct {
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
	Total      *int   `json:"total,omitempty"`
}

// OK sends a 200 OK response with data and no pagination metadata.
func OK(c *fiber.Ctx, data interface{}) error {
	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"data":  data,
		"error": nil,
		"meta":  nil,
	})
}

// Created sends a 201 Created response with the newly created resource.
func Created(c *fiber.Ctx, data interface{}) error {
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"data":  data,
		"error": nil,
		"meta":  nil,
	})
}

// NoContent sends a 204 No Content response with no body.
func NoContent(c *fiber.Ctx) error {
	return c.SendStatus(fiber.StatusNoContent)
}

// List sends a 200 OK response with a data array and keyset pagination metadata.
func List(c *fiber.Ctx, data interface{}, meta *Meta) error {
	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"data":  data,
		"error": nil,
		"meta":  meta,
	})
}
