const dtp = require("dtp");

// device01;secret123;true in devices.csv
const client = dtp.newClient("127.0.0.1:8091", "e0be951a-7b3c-4428-868c-26732f7881c0", "secret123", true);

client.on("connect", () => {
    print("DTP Client Connected!");
});

client.on("ack", (packet) => {
    print("Received ACK for packet ID:", packet.ID);
});

client.on("error", (err) => {
    print("DTP Client Error:", err);
});

client.connect(); // Synchronous handshake
client.sendData("SENSOR_DATA", "Temperature: 22.5C", true); // Synchronous ACK wait

print("DTP Test Step 1 completed");
