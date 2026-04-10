//go:build !windows

package main

import (
	"encoding/json"
	"fmt"
	"http-server/plugins/config"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)
// --------------------------------------------------------------------------
// scanVhosts
// --------------------------------------------------------------------------

// helper : crée un vhost subfolder avec un fichier .vhost.bind (modern) ou .vhost (legacy).
func mkVhost(t *testing.T, base, name, vhostContent string) {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if vhostContent != "" {
		filename := ".vhost.bind"
		// If it looks like legacy HCL, use .vhost
		if strings.Contains(vhostContent, "=") && !strings.Contains(vhostContent, "HTTP") {
			filename = ".vhost"
		}
		if err := os.WriteFile(filepath.Join(dir, filename), []byte(vhostContent), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScanVhosts_NoConfig(t *testing.T) {
	tmp := t.TempDir()
	mkVhost(t, tmp, "mysite", "")

	vhosts, err := scanVhosts(tmp, 8080)
	if err != nil {
		t.Fatal(err)
	}
	if len(vhosts) != 1 {
		t.Fatalf("expected 1 vhost, got %d", len(vhosts))
	}
	v := vhosts[0]
	if v.Domain != "mysite" {
		t.Errorf("expected domain 'mysite', got %q", v.Domain)
	}
	if len(v.Aliases) != 0 {
		t.Errorf("expected no aliases, got %v", v.Aliases)
	}
	if len(v.Listens) != 1 {
		t.Fatalf("expected 1 listen, got %d", len(v.Listens))
	}
	if v.Listens[0].Protocol != "http" {
		t.Errorf("expected protocol 'http', got %q", v.Listens[0].Protocol)
	}
	if v.Listens[0].Port != 8080 {
		t.Errorf("expected port 8080, got %d", v.Listens[0].Port)
	}
}

func TestScanVhosts_WithDomainAndAliases(t *testing.T) {
	tmp := t.TempDir()
	mkVhost(t, tmp, "site-a", `HTTP example.com
  DOMAIN example.com
  ALIASES www.example.com
  ALIASES ex.com
END HTTP`)

	vhosts, err := scanVhosts(tmp, 8080)
	if err != nil {
		t.Fatal(err)
	}
	if len(vhosts) != 1 {
		t.Fatalf("expected 1 vhost, got %d", len(vhosts))
	}
	v := vhosts[0]
	if v.Domain != "example.com" {
		t.Errorf("expected domain 'example.com', got %q", v.Domain)
	}
	if len(v.Aliases) != 2 {
		t.Fatalf("expected 2 aliases, got %d", len(v.Aliases))
	}
	if v.Aliases[0] != "www.example.com" || v.Aliases[1] != "ex.com" {
		t.Errorf("unexpected aliases: %v", v.Aliases)
	}
}

func TestScanVhosts_HTTPBlock(t *testing.T) {
	tmp := t.TempDir()
	mkVhost(t, tmp, "web", `HTTP web.local
  PORT 8080
END HTTP`)

	vhosts, err := scanVhosts(tmp, 3000)
	if err != nil {
		t.Fatal(err)
	}
	v := vhosts[0]
	if len(v.Listens) != 1 {
		t.Fatalf("expected 1 listen, got %d", len(v.Listens))
	}
	l := v.Listens[0]
	if l.Protocol != "http" {
		t.Errorf("expected 'http', got %q", l.Protocol)
	}
	if l.Port != 8080 {
		t.Errorf("expected port 8080, got %d", l.Port)
	}
}

func TestScanVhosts_HTTPSBlock(t *testing.T) {
	tmp := t.TempDir()
	mkVhost(t, tmp, "secure", `HTTPS secure.local
  PORT 443
  SSL /etc/ssl/cert.pem /etc/ssl/key.pem
  EMAIL admin@secure.local
END HTTPS`)

	vhosts, err := scanVhosts(tmp, 8080)
	if err != nil {
		t.Fatal(err)
	}
	v := vhosts[0]
	if len(v.Listens) != 1 {
		t.Fatalf("expected 1 listen, got %d", len(v.Listens))
	}
	l := v.Listens[0]
	if l.Protocol != "https" {
		t.Errorf("expected 'https', got %q", l.Protocol)
	}
	if v.Cert != "/etc/ssl/cert.pem" {
		t.Errorf("expected cert path, got %q", v.Cert)
	}
	if v.Email != "admin@secure.local" {
		t.Errorf("expected email 'admin@secure.local', got %q", v.Email)
	}
}

func TestScanVhosts_HTTPAndHTTPSBlocks(t *testing.T) {
	tmp := t.TempDir()
	mkVhost(t, tmp, "dual", `HTTP dual.local
  PORT 80
END HTTP
HTTPS dual.local
  PORT 443
END HTTPS`)

	vhosts, err := scanVhosts(tmp, 8080)
	if err != nil {
		t.Fatal(err)
	}
	v := vhosts[0]
	if len(v.Listens) != 2 {
		t.Fatalf("expected 2 listens, got %d", len(v.Listens))
	}
	// http comes first, then https
	if v.Listens[0].Protocol != "http" {
		t.Errorf("expected first listen 'http', got %q", v.Listens[0].Protocol)
	}
	if v.Listens[1].Protocol != "https" {
		t.Errorf("expected second listen 'https', got %q", v.Listens[1].Protocol)
	}
}

func TestScanVhosts_FallbackPortFromConfig(t *testing.T) {
	tmp := t.TempDir()
	mkVhost(t, tmp, "custom-port", `HTTP custom.local
  PORT 9000
END HTTP`)

	vhosts, err := scanVhosts(tmp, 8080)
	if err != nil {
		t.Fatal(err)
	}
	v := vhosts[0]
	if len(v.Listens) != 1 {
		t.Fatalf("expected 1 listen, got %d", len(v.Listens))
	}
	if v.Listens[0].Port != 9000 {
		t.Errorf("expected port 9000, got %d", v.Listens[0].Port)
	}
}

func TestScanVhosts_IgnoresFiles(t *testing.T) {
	tmp := t.TempDir()
	mkVhost(t, tmp, "real-site", "")
	// Write a plain file (not a directory) — should be ignored
	if err := os.WriteFile(filepath.Join(tmp, "not-a-dir.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	vhosts, err := scanVhosts(tmp, 8080)
	if err != nil {
		t.Fatal(err)
	}
	if len(vhosts) != 1 {
		t.Fatalf("expected 1 vhost (file ignored), got %d", len(vhosts))
	}
	if vhosts[0].Domain != "real-site" {
		t.Errorf("expected domain 'real-site', got %q", vhosts[0].Domain)
	}
}

func TestScanVhosts_InvalidConfig(t *testing.T) {
	tmp := t.TempDir()
	mkVhost(t, tmp, "broken", "THIS IS NOT VALID HCL }{}{")

	vhosts, err := scanVhosts(tmp, 8080)
	if err != nil {
		t.Fatal(err)
	}
	// Should still produce a result with fallback defaults
	if len(vhosts) != 1 {
		t.Fatalf("expected 1 vhost (fallback), got %d", len(vhosts))
	}
	v := vhosts[0]
	if v.Domain != "broken" {
		t.Errorf("expected domain 'broken' (folder name fallback), got %q", v.Domain)
	}
	if v.Listens[0].Port != 8080 {
		t.Errorf("expected default port 8080, got %d", v.Listens[0].Port)
	}
}

func TestScanVhosts_EmptyDir(t *testing.T) {
	tmp := t.TempDir()

	vhosts, err := scanVhosts(tmp, 8080)
	if err != nil {
		t.Fatal(err)
	}
	if len(vhosts) != 0 {
		t.Errorf("expected 0 vhosts, got %d", len(vhosts))
	}
}

func TestScanVhosts_MultipleSites(t *testing.T) {
	tmp := t.TempDir()
	mkVhost(t, tmp, "alpha", `HTTP alpha.com
END HTTP`)
	mkVhost(t, tmp, "beta", `HTTP beta.com
END HTTP`)
	mkVhost(t, tmp, "gamma", "")

	vhosts, err := scanVhosts(tmp, 8080)
	if err != nil {
		t.Fatal(err)
	}
	if len(vhosts) != 3 {
		t.Fatalf("expected 3 vhosts, got %d", len(vhosts))
	}
	domains := map[string]bool{}
	for _, v := range vhosts {
		domains[v.Domain] = true
	}
	for _, expected := range []string{"alpha.com", "beta.com", "gamma"} {
		if !domains[expected] {
			t.Errorf("expected domain %q not found", expected)
		}
	}
}

func TestScanVhosts_CertFallback(t *testing.T) {
	tmp := t.TempDir()
	mkVhost(t, tmp, "tls-site", `HTTPS tls.local
  PORT 443
  SSL cert.pem key.pem
END HTTPS`)

	vhosts, err := scanVhosts(tmp, 8080)
	if err != nil {
		t.Fatal(err)
	}
	v := vhosts[0]
	if v.Listens[0].Protocol != "https" {
		t.Errorf("expected 'https' when cert+key are set, got %q", v.Listens[0].Protocol)
	}
	if v.Cert != "cert.pem" {
		t.Errorf("expected global cert.pem, got %q", v.Cert)
	}
}

func TestScanVhosts_MultipleVHostBind(t *testing.T) {
	tmp := t.TempDir()
	mkVhost(t, tmp, "site1", "HTTP site1.local\n  GET / FILE ./site1\nEND HTTP")
	// Also test that .vhost.bind is preferred over .vhost (which is empty here)
	dir2 := filepath.Join(tmp, "site2")
	os.MkdirAll(dir2, 0755)
	os.WriteFile(filepath.Join(dir2, ".vhost.bind"), []byte("HTTP site2.local\n  GET / FILE ./site2\nEND HTTP"), 0644)
	os.WriteFile(filepath.Join(dir2, ".vhost"), []byte("domain = \"wrong.local\""), 0644)

	vhosts, err := scanVhosts(tmp, 8080)
	if err != nil {
		t.Fatal(err)
	}
	if len(vhosts) != 2 {
		t.Fatalf("expected 2 vhosts, got %d", len(vhosts))
	}
	
	domains := map[string]bool{}
	for _, v := range vhosts {
		domains[v.Domain] = true
	}
	if !domains["site1.local"] || !domains["site2.local"] {
		t.Errorf("expected domains site1.local and site2.local, got %v", domains)
	}
}

// --------------------------------------------------------------------------
// Helper functions
// --------------------------------------------------------------------------

func TestNormalizeSocketPath(t *testing.T) {
	path := "/tmp/my-socket.sock"
	got := normalizeSocketPath(path)
	if got != path {
		t.Errorf("on Unix, expected path unchanged %q, got %q", path, got)
	}
}

func TestGetSocketNetwork(t *testing.T) {
	if got := getSocketNetwork("/tmp/foo.sock"); got != "unix" {
		t.Errorf("expected 'unix', got %q", got)
	}
}

func TestGetInternalSocketPath(t *testing.T) {
	got := getInternalSocketPath(0)
	expected := filepath.Join(os.TempDir(), "http-server-0.sock")
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
	got1 := getInternalSocketPath(1)
	if got == got1 {
		t.Error("expected different paths for index 0 and 1")
	}
}

func TestUDPProxy_BiDirectional(t *testing.T) {
	// 1. Setup a "worker" listening on a Unixgram socket
	sockPath := filepath.Join(t.TempDir(), "worker.sock")
	ln, err := net.ListenPacket("unixgram", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	// 1.5 Worker Loop (handles both validation and data)
	go func() {
		buf := make([]byte, 64*1024)
		for {
			n, addr, err := ln.ReadFrom(buf)
			if err != nil {
				return
			}
			
			// Try to decode as IPC request
			var req struct {
				Proto string `json:"proto"`
				Port  string `json:"port"`
				Data  []byte `json:"data"`
			}
			if err := json.Unmarshal(buf[:n], &req); err == nil && req.Proto == "udp" {
				// Validation request
				resp, _ := json.Marshal(map[string]bool{"ok": true})
				ln.WriteTo(resp, addr)
				continue
			}

			// Data packet
			ln.WriteTo(append([]byte("ECHO: "), buf[:n]...), addr)
		}
	}()

	// 2. Setup the Proxy
	cfg := &config.AppConfig{Address: "127.0.0.1", Silent: true}
	// We'll use a fixed port for simplicity in this test
	proxyPort := 9999
	workers := map[string]string{"test.local": sockPath}
	
	// Start proxy in background
	go func() {
		_ = runUDPProxy(proxyPort, workers, cfg)
	}()
	time.Sleep(100 * time.Millisecond) // wait for server

	// 3. Client: Send and Receive
	c, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", proxyPort))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	msg := []byte("hello world")
	c.Write(msg)

	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp := make([]byte, 1024)
	n, err := c.Read(resp)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	expected := "ECHO: hello world"
	if string(resp[:n]) != expected {
		t.Errorf("expected %q, got %q", expected, string(resp[:n]))
	}
}

// --------------------------------------------------------------------------
// getAvailableIPs
// --------------------------------------------------------------------------

func TestGetAvailableIPs_SpecificAddr(t *testing.T) {
	ips := getAvailableIPs("192.168.1.100")
	if len(ips) != 1 {
		t.Fatalf("expected 1 IP, got %d", len(ips))
	}
	if ips[0] != "192.168.1.100" {
		t.Errorf("expected '192.168.1.100', got %q", ips[0])
	}
}

func TestGetAvailableIPs_Wildcard(t *testing.T) {
	ips := getAvailableIPs("0.0.0.0")
	if len(ips) == 0 {
		t.Fatal("expected at least one IP for wildcard bind")
	}
	// Should always include 127.0.0.1
	found := false
	for _, ip := range ips {
		if ip == "127.0.0.1" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 127.0.0.1 in results, got %v", ips)
	}
}

func TestGetAvailableIPs_IPv6Wildcard(t *testing.T) {
	ips := getAvailableIPs("::")
	if len(ips) == 0 {
		t.Fatal("expected at least one IP for IPv6 wildcard")
	}
}

// --------------------------------------------------------------------------
// runControlSocket (IPC handshake)
// --------------------------------------------------------------------------

func TestRunControlSocket_IPCHandshake(t *testing.T) {
	sockPath := filepath.Join(os.TempDir(), fmt.Sprintf("hs-test-ctrl-%d.sock", time.Now().UnixNano()))
	defer os.Remove(sockPath)

	cfg := &config.AppConfig{
		ControlSocket: sockPath,
		Silent:        true,
	}

	// Start control socket with nil manager (no binder)
	go runControlSocket(cfg, nil)
	time.Sleep(100 * time.Millisecond)

	// Connect and send a validation request
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Failed to connect to control socket: %v", err)
	}
	defer conn.Close()

	// Send IPC request
	req := struct {
		Proto string `json:"proto"`
		Port  string `json:"port"`
		Data  []byte `json:"data"`
	}{
		Proto: "tcp",
		Port:  "8080",
		Data:  []byte("test"),
	}
	json.NewEncoder(conn).Encode(req)

	// Read response
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var resp struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// With nil manager, validation should return false
	if resp.OK {
		t.Error("Expected ok=false with nil manager")
	}
}

// --------------------------------------------------------------------------
// validateAndInject (TCP IPC)
// --------------------------------------------------------------------------

func TestValidateAndInject_TCPValidation(t *testing.T) {
	sockPath := filepath.Join(os.TempDir(), fmt.Sprintf("hs-test-vi-%d.sock", time.Now().UnixNano()))
	defer os.Remove(sockPath)

	// Setup a mock worker that accepts TCP validation
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				var req struct {
					Proto string `json:"proto"`
					Port  string `json:"port"`
					Data  []byte `json:"data"`
				}
				if err := json.NewDecoder(c).Decode(&req); err != nil {
					return
				}
				// Always validate successfully
				json.NewEncoder(c).Encode(map[string]bool{"ok": true})
			}(conn)
		}
	}()
	time.Sleep(50 * time.Millisecond)

	// Test: validate with nil rawConn (UDP-style, no FD passing)
	result := validateAndInject(sockPath, "tcp", 8080, []byte("hello"), nil)
	if !result {
		t.Error("Expected validation to succeed")
	}
}

func TestValidateAndInject_BadSocket(t *testing.T) {
	// Non-existent socket should fail gracefully
	result := validateAndInject("/nonexistent/socket.sock", "tcp", 8080, []byte("test"), nil)
	if result {
		t.Error("Expected validation to fail for non-existent socket")
	}
}

func TestValidateAndInject_RejectedValidation(t *testing.T) {
	sockPath := filepath.Join(os.TempDir(), fmt.Sprintf("hs-test-rej-%d.sock", time.Now().UnixNano()))
	defer os.Remove(sockPath)

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				var req struct {
					Proto string `json:"proto"`
					Port  string `json:"port"`
					Data  []byte `json:"data"`
				}
				json.NewDecoder(c).Decode(&req)
				// Reject the validation
				json.NewEncoder(c).Encode(map[string]bool{"ok": false})
			}(conn)
		}
	}()
	time.Sleep(50 * time.Millisecond)

	result := validateAndInject(sockPath, "tcp", 8080, []byte("hello"), nil)
	if result {
		t.Error("Expected validation to be rejected")
	}
}

// --------------------------------------------------------------------------
// Additional scanVhosts edge cases
// --------------------------------------------------------------------------

func TestScanVhosts_TCPBlock(t *testing.T) {
	tmp := t.TempDir()
	mkVhost(t, tmp, "tcp-site", `TCP tcp.local
  PORT 9090
END TCP`)

	vhosts, err := scanVhosts(tmp, 8080)
	if err != nil {
		t.Fatal(err)
	}
	if len(vhosts) != 1 {
		t.Fatalf("expected 1 vhost, got %d", len(vhosts))
	}
	v := vhosts[0]
	if len(v.Listens) != 1 {
		t.Fatalf("expected 1 listen, got %d", len(v.Listens))
	}
	if v.Listens[0].Protocol != "tcp" {
		t.Errorf("expected protocol 'tcp', got %q", v.Listens[0].Protocol)
	}
	if v.Listens[0].Port != 9090 {
		t.Errorf("expected port 9090, got %d", v.Listens[0].Port)
	}
}

func TestScanVhosts_UDPBlock(t *testing.T) {
	tmp := t.TempDir()
	mkVhost(t, tmp, "udp-site", `UDP udp.local
  PORT 5000
END UDP`)

	vhosts, err := scanVhosts(tmp, 8080)
	if err != nil {
		t.Fatal(err)
	}
	v := vhosts[0]
	if v.Listens[0].Protocol != "udp" {
		t.Errorf("expected protocol 'udp', got %q", v.Listens[0].Protocol)
	}
	if v.Listens[0].Port != 5000 {
		t.Errorf("expected port 5000, got %d", v.Listens[0].Port)
	}
}

func TestScanVhosts_MultiProtocol(t *testing.T) {
	tmp := t.TempDir()
	mkVhost(t, tmp, "multi", `HTTP multi.local
  PORT 80
END HTTP
TCP multi.local
  PORT 9090
END TCP
UDP multi.local
  PORT 5000
END UDP`)

	vhosts, err := scanVhosts(tmp, 8080)
	if err != nil {
		t.Fatal(err)
	}
	v := vhosts[0]
	if len(v.Listens) != 3 {
		t.Fatalf("expected 3 listens (http+tcp+udp), got %d", len(v.Listens))
	}
	protos := map[string]bool{}
	for _, l := range v.Listens {
		protos[l.Protocol] = true
	}
	for _, p := range []string{"http", "tcp", "udp"} {
		if !protos[p] {
			t.Errorf("expected protocol %q in listens", p)
		}
	}
}

func TestScanVhosts_AllDirsEnumerated(t *testing.T) {
	tmp := t.TempDir()
	mkVhost(t, tmp, "alpha", "")
	mkVhost(t, tmp, "beta", "")
	mkVhost(t, tmp, "gamma", "")

	vhosts, err := scanVhosts(tmp, 8080)
	if err != nil {
		t.Fatal(err)
	}
	if len(vhosts) != 3 {
		t.Fatalf("expected 3 vhosts, got %d", len(vhosts))
	}
	domains := map[string]bool{}
	for _, v := range vhosts {
		domains[v.Domain] = true
	}
	for _, d := range []string{"alpha", "beta", "gamma"} {
		if !domains[d] {
			t.Errorf("expected domain %q in results", d)
		}
	}
}

