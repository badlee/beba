const WebSocket = require('ws');
const ws = new WebSocket('ws://localhost:9999/scripted');

ws.on('open', () => {
    console.log('Connected');
    // Standardized message structure: {id: string, channel: string?, data: any}
    ws.send(JSON.stringify({
        id: "1",
        channel: "global",
        data: "Hello from client"
    }));
});

ws.on('message', (data) => {
    console.log('Received:', data.toString());
    if (data.toString().includes("ECHO")) {
        console.log("Success: Received Echo");
        process.exit(0);
    }
});

setTimeout(() => {
    console.log('Timeout');
    process.exit(1);
}, 5000);
