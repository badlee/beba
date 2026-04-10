/**
 * Custom DTP (Device Transport Protocol) implementation
 * Example showing how to use the new function-based JS binder.
 */

// match(buffer) returns true if this protocol should handle the connection
function match(buffer) {
    const view = new Uint8Array(buffer);
    // Let's say our protocol starts with 'DTP' (0x44, 0x54, 0x50)
    return view[0] === 0x44 && view[1] === 0x54 && view[2] === 0x50;
}

// handle() is called if match() returns true
function handle() {
    print("DTP protocol handler started (Node.js style)!");

    // Use the new event-driven API directly on the socket
    socket.on("data", (data) => {
        print("Received data: " + data);
        if (data.includes("PING")) {
            socket.write("PONG\n");
        } else if (data.includes("QUIT")) {
            print("Closing connection");
            socket.end();
        }
    });

    socket.on("error", (err) => {
        print("Socket error: " + err);
    });

    socket.on("close", () => {
        print("Socket closed");
    });
}
