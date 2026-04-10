// Handler ERROR externe JS – examples/handlers/error_handler.js
// Utilisé par: ERROR 503 HANDLER examples/handlers/error_handler.js

context.Status(503).JSON({
    error: true,
    code: 503,
    message: "Service temporairement indisponible",
    path: context.Path(),
    method: context.Method()
});
