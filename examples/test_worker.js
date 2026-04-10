// Test worker: expose les variables config et settings
// Lancé avec: WORKER examples/test_worker.js INTERVAL=1000 TARGET="localhost"

const interval = parseInt(config.INTERVAL) || 5000;
const target = config.TARGET || "unknown";
const appName = settings.APP_NAME || "unnamed";

print("[Worker] Started for app: " + appName);
print("[Worker] Will monitor: " + target + " every " + interval + "ms");

// Simuler une tâche répétitive (dans un vrai contexte, on utiliserait setInterval)
let count = 0;
for (let i = 0; i < 3; i++) {
    count++;
    print("[Worker] Tick #" + count + " - checking " + target);
}

print("[Worker] Done. settings.API_VERSION = " + (settings.API_VERSION || "N/A"));
