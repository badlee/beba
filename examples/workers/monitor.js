// Test Worker – expose config et settings
// Usage dans .bind: WORKER examples/workers/monitor.js INTERVAL=2000 TARGET="http://example.com" LABEL="MyWorker"
// Les variables config (KEY=VALUE du WORKER) et settings (SET) sont injectées auto.

const label = config.LABEL || "worker";
const target = config.TARGET || "http://localhost";
const interval = parseInt(config.INTERVAL) || 5000;
const appName = (settings && settings.APP_NAME) || "unknown-app";

print(`[${label}] Démarrage pour l'app: ${appName}`);
print(`[${label}] Surveillance de: ${target} (intervalle: ${interval}ms)`);

// Simuler quelques cycles de monitoring
for (let i = 1; i <= 3; i++) {
    print(`[${label}] Cycle #${i}: vérification de ${target}`);
}

print(`[${label}] Worker terminé (3 cycles). API_VERSION=${settings?.API_VERSION || "N/A"}`);
