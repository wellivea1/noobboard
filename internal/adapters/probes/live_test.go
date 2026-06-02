package probes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLiveProbeClientChecksHTTPDNSAndTargets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewLiveClient(LiveConfig{
		InternetURL:  server.URL,
		DNSHost:      "localhost",
		RouterTarget: server.URL,
		NASTarget:    server.URL,
	})
	infra, err := client.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !infra.InternetReachable || !infra.DNSOK || !infra.RouterReachable || !infra.NASReachable {
		t.Fatalf("expected all probes healthy: %#v", infra)
	}
	for _, part := range []string{"internet ok", "dns ok", "router ok", "nas ok"} {
		if !strings.Contains(infra.SourceHealth.Probes, part) {
			t.Fatalf("source health %q missing %q", infra.SourceHealth.Probes, part)
		}
	}
}

func TestLiveProbeClientMarksSkippedTargets(t *testing.T) {
	client := NewLiveClient(LiveConfig{DNSHost: "localhost"})
	infra, err := client.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"internet skipped", "router skipped", "nas skipped"} {
		if !strings.Contains(infra.SourceHealth.Probes, part) {
			t.Fatalf("source health %q missing %q", infra.SourceHealth.Probes, part)
		}
	}
	if !infra.DNSOK {
		t.Fatalf("expected localhost DNS probe to pass: %#v", infra)
	}
}

func TestLiveProbeClientRequiresTargets(t *testing.T) {
	client := NewLiveClient(LiveConfig{})
	if _, err := client.Status(t.Context()); err == nil {
		t.Fatal("expected no-target live probe config to fail")
	}
}
