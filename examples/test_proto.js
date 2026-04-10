function match(buf) {
    var uint8 = new Uint8Array(buf);
    var s = "";
    for (var i = 0; i < Math.min(uint8.length, 3); i++) {
        s += String.fromCharCode(uint8[i]);
    }
    return s === "DTP";
}

function handle() {
    socket.Write("Hello from DTP Protocol over JS!\n");
    socket.End();
}
