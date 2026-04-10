// Ce middleware s'applique à toutes les routes de site-a.local
const start = Date.now();
context.Locals("startTime", start);
context.Set("X-Vhost-Router", "FsRouter");
print("Middleware appelé sur " + context.Path());
