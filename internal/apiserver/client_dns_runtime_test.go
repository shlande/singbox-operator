package apiserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Opt-in integration test against a real sing-box client. All DNS, proxies and
// application traffic stay on loopback; no TUN, privileges or cluster is used.
// SING_BOX_BINARY=/path/to/sing-box go test ./internal/apiserver -run TestClientDNSRuntime -v
func TestClientDNSRuntime(t *testing.T) {
	binary := os.Getenv("SING_BOX_BINARY")
	if binary == "" {
		t.Skip("set SING_BOX_BINARY to validate live Clash mode switching")
	}
	var mu sync.Mutex
	queries := make(map[string]map[string]int)
	newDoH := func(region string, lastOctet byte) *httptest.Server {
		queries[region] = make(map[string]int)
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "read DNS query", http.StatusBadRequest)
				return
			}
			var query dnsmessage.Message
			if err := query.Unpack(body); err != nil || len(query.Questions) != 1 {
				http.Error(w, "invalid DNS query", http.StatusBadRequest)
				return
			}
			question := query.Questions[0]
			mu.Lock()
			queries[region][question.Name.String()]++
			mu.Unlock()
			answer := [4]byte{192, 0, 2, lastOctet}
			if question.Name.String() == "direct.test." {
				answer = [4]byte{127, 0, 0, 1}
			}
			query.Response = true
			query.RecursionAvailable = true
			if question.Type == dnsmessage.TypeA {
				query.Answers = []dnsmessage.Resource{{
					Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 300},
					Body:   &dnsmessage.AResource{A: answer},
				}}
			}
			packed, err := query.Pack()
			if err != nil {
				http.Error(w, "pack DNS response", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/dns-message")
			_, _ = w.Write(packed)
		}))
		t.Cleanup(server.Close)
		return server
	}
	fallback := newDoH("fallback", 100)
	_, dnsPortText, err := net.SplitHostPort(fallback.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	dnsPort, err := strconv.Atoi(dnsPortText)
	if err != nil {
		t.Fatal(err)
	}
	outbounds := make([]any, 0, 1+2*len(clientRegionModes))
	outbounds = append(outbounds, map[string]any{"type": "direct", "tag": "direct"})
	for i, mode := range clientRegionModes {
		doh := newDoH(mode.tag, byte(i+1))
		proxy := newRuntimeDNSProxy(t, doh.Listener.Addr().String())
		proxyAddr := proxy.Listener.Addr().(*net.TCPAddr)
		nodeTag := mode.tag + "-node"
		outbounds = append(outbounds,
			map[string]any{"type": "http", "tag": nodeTag, "server": "127.0.0.1", "server_port": proxyAddr.Port},
			map[string]any{"type": "selector", "tag": mode.tag, "outbounds": []string{nodeTag}},
		)
	}
	apiPort, inboundPort, queryPort := unusedTCPPort(t), unusedTCPPort(t), unusedTCPPort(t)
	template, err := json.Marshal(map[string]any{
		"log": map[string]any{"level": "debug", "timestamp": false},
		"dns": map[string]any{
			"servers": []any{
				map[string]any{"type": "https", "tag": proxyDNSTag, "server": "127.0.0.1", "server_port": dnsPort, "tls": map[string]any{"insecure": true}},
				map[string]any{"type": "hosts", "tag": localDNSTag, "predefined": map[string]any{"domestic.test": "192.0.2.200"}},
			},
			"rules":    []any{map[string]any{"domain": "domestic.test", "server": localDNSTag}},
			"strategy": "ipv4_only",
		},
		"inbounds": []any{
			map[string]any{"type": "direct", "tag": "dns-in", "listen": "127.0.0.1", "listen_port": queryPort, "network": "udp"},
			map[string]any{"type": "http", "tag": "http-in", "listen": "127.0.0.1", "listen_port": inboundPort},
		},
		"route": map[string]any{
			"rules": []any{
				map[string]any{"action": "hijack-dns", "inbound": "dns-in"},
				map[string]any{"rule_set": []string{"geosite-cn"}, "outbound": "direct"},
			},
			// This domain is directly connected, but is intentionally NOT a local
			// DNS exception: its dial resolution must follow the selected mode.
			"rule_set": []any{map[string]any{"type": "inline", "tag": "geosite-cn", "rules": []any{map[string]any{"domain": "direct.test"}}}},
		},
		"experimental": map[string]any{"clash_api": map[string]any{"external_controller": fmt.Sprintf("127.0.0.1:%d", apiPort)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	config, err := MergeClientConfig(template, outbounds, ClientConfigInput{})
	if err != nil {
		t.Fatal(err)
	}
	apiURL := fmt.Sprintf("http://127.0.0.1:%d/configs", apiPort)
	apiClient := startClientDNSRuntime(t, binary, config, apiURL)
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer app.Close()
	proxyURL, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", inboundPort))
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	appClient := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	appURL := fmt.Sprintf("http://direct.test:%d/", app.Listener.Addr().(*net.TCPAddr).Port)
	for _, tc := range []struct {
		mode   string
		answer string
	}{
		{clientModeAuto, "192.0.2.1"}, {"HK", "192.0.2.1"}, {"JP", "192.0.2.2"},
		{"US", "192.0.2.3"}, {"JP", "192.0.2.2"}, {clientModeAuto, "192.0.2.1"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			body, err := json.Marshal(map[string]string{"mode": tc.mode})
			if err != nil {
				t.Fatal(err)
			}
			req, err := http.NewRequest(http.MethodPatch, apiURL, bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := apiClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				t.Fatalf("change Clash mode: HTTP %d", resp.StatusCode)
			}
			// Query the SAME name across modes to catch stale cross-region caches.
			if got := runtimeDNSQuery(t, queryPort, "google.test."); got != tc.answer {
				t.Fatalf("DNS answer = %s, want %s", got, tc.answer)
			}
			if got := runtimeDNSQuery(t, queryPort, "domestic.test."); got != "192.0.2.200" {
				t.Fatalf("domestic DNS answer = %s", got)
			}
			resp, err = appClient.Get(appURL)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("direct domain request: HTTP %d", resp.StatusCode)
			}
		})
	}
	mu.Lock()
	for _, mode := range clientRegionModes {
		if queries[mode.tag]["direct.test."] == 0 {
			t.Errorf("direct destination resolution bypassed DNS mode %s", mode.name)
		}
		if queries[mode.tag]["domestic.test."] != 0 {
			t.Errorf("domestic DNS escaped local resolver in mode %s", mode.name)
		}
	}
	if len(queries["fallback"]) != 0 {
		t.Errorf("DNS unexpectedly fell back to direct DoH: %v", queries["fallback"])
	}
	mu.Unlock()
	t.Run("no-region-direct-doh", func(t *testing.T) {
		testClientDNSNoRegionRuntime(t, binary, template)
	})
}

// Redirect CONNECT tunnels to a region-specific local DoH endpoint so the
// returned DNS answer proves which selector was used, without external traffic.
func newRuntimeDNSProxy(t *testing.T, address string) *httptest.Server {
	t.Helper()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT required", http.StatusMethodNotAllowed)
			return
		}
		upstream, dialErr := net.DialTimeout("tcp", address, 3*time.Second)
		if dialErr != nil {
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
			return
		}
		defer func() { _ = upstream.Close() }()
		conn, rw, hijackErr := w.(http.Hijacker).Hijack()
		if hijackErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		if _, err := rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		if err := rw.Flush(); err != nil {
			return
		}
		go func() { _, _ = io.Copy(upstream, rw) }()
		_, _ = io.Copy(conn, upstream)
	}))
	t.Cleanup(proxy.Close)
	return proxy
}

func testClientDNSNoRegionRuntime(t *testing.T, binary string, template []byte) {
	t.Helper()
	var config map[string]any
	if err := json.Unmarshal(template, &config); err != nil {
		t.Fatal(err)
	}
	port, apiPort := unusedTCPPort(t), unusedTCPPort(t)
	config["inbounds"] = []any{map[string]any{
		"type": "direct", "tag": "dns-in", "listen": "127.0.0.1", "listen_port": port, "network": "udp",
	}}
	clash := config["experimental"].(map[string]any)["clash_api"].(map[string]any)
	clash["external_controller"] = fmt.Sprintf("127.0.0.1:%d", apiPort)
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	data, err = MergeClientConfig(data, []any{map[string]any{"type": "direct", "tag": "direct"}}, ClientConfigInput{})
	if err != nil {
		t.Fatal(err)
	}
	startClientDNSRuntime(t, binary, data, fmt.Sprintf("http://127.0.0.1:%d/configs", apiPort))
	if got := runtimeDNSQuery(t, port, "google.test."); got != "192.0.2.100" {
		t.Fatalf("no-region DNS did not use direct DoH: %s", got)
	}
	if got := runtimeDNSQuery(t, port, "domestic.test."); got != "192.0.2.200" {
		t.Fatalf("no-region DNS lost local exception: %s", got)
	}
}

func startClientDNSRuntime(t *testing.T, binary string, config []byte, apiURL string) *http.Client {
	t.Helper()
	path := filepath.Join(t.TempDir(), "client.json")
	if err := os.WriteFile(path, config, 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(binary, "check", "-c", path).CombinedOutput(); err != nil {
		t.Fatalf("sing-box check: %v\n%s", err, output)
	}
	logPath := filepath.Join(t.TempDir(), "sing-box.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "run", "-c", path)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = logFile.Close()
		if t.Failed() {
			output, _ := os.ReadFile(logPath)
			t.Logf("sing-box log:\n%s", output)
		}
	})
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := client.Get(apiURL)
		if err == nil {
			_ = resp.Body.Close()
			return client
		}
		if time.Now().After(deadline) {
			t.Fatal("Clash API failed to start")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func unusedTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func runtimeDNSQuery(t *testing.T, port int, domain string) string {
	t.Helper()
	name, err := dnsmessage.NewName(domain)
	if err != nil {
		t.Fatal(err)
	}
	query := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: 42, RecursionDesired: true},
		Questions: []dnsmessage.Question{{Name: name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}},
	}
	packet, err := query.Pack()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("udp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(packet); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 4096)
	n, err := conn.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	var response dnsmessage.Message
	if err := response.Unpack(buffer[:n]); err != nil {
		t.Fatal(err)
	}
	for _, answer := range response.Answers {
		if a, ok := answer.Body.(*dnsmessage.AResource); ok {
			return net.IP(a.A[:]).String()
		}
	}
	t.Fatalf("no DNS A answer for %s: %v", domain, response.Header)
	return ""
}
