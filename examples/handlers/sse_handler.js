// Handler SSE externe – examples/handlers/sse_handler.js
// Accessible en HTTP via SSE "/live" HANDLER examples/handlers/sse_handler.js
// Le module sse est disponible via require("sse")

const sse = require("sse");

// Publier un événement initial
sse.publish("connect", {
    message: "Client connecté au stream live",
    ts: Date.now()
});

// Note: dans un contexte de production, on utiliserait
// un worker ou un système de pub/sub pour alimenter le stream.
print("SSE handler exécuté pour: " + context.Path());
