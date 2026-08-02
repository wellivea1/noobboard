package probes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wellivea1/noobboard/internal/models"
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

func TestStatusRecordsLatencyPerProbe(t *testing.T) {
	// Reachability booleans could not answer "is it slow". Every probe is now
	// timed on the same request, so the measurement costs nothing extra.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewLiveClient(LiveConfig{InternetURL: server.URL, RouterTarget: server.URL, Timeout: 2 * time.Second})
	infra, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(infra.ProbeLatencies) != 2 {
		t.Fatalf("probe latencies = %#v, want one per configured target", infra.ProbeLatencies)
	}
	subjects := map[string]models.ProbeLatency{}
	for _, probe := range infra.ProbeLatencies {
		subjects[probe.Subject] = probe
	}
	internet, ok := subjects[ProbeInternet]
	if !ok || !internet.OK {
		t.Fatalf("internet probe = %#v, want a successful reading", internet)
	}
	// Baseline and failure rate are the server's to fill from its window; the
	// adapter must not invent them.
	if internet.BaselineMS != 0 || internet.SampleCount != 0 || internet.FailureRate != 0 {
		t.Fatalf("adapter filled server-owned fields: %#v", internet)
	}
}

func TestStatusRecordsAFailedProbeWithoutClaimingItIsFast(t *testing.T) {
	// A failed probe measures the timeout, not the link. It must still be
	// recorded so the failure rate can see it, but marked not-OK so the baseline
	// excludes it.
	client := NewLiveClient(LiveConfig{RouterTarget: "127.0.0.1:1", Timeout: 200 * time.Millisecond})
	infra, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(infra.ProbeLatencies) != 1 || infra.ProbeLatencies[0].OK {
		t.Fatalf("probe latencies = %#v, want one failed reading", infra.ProbeLatencies)
	}
}
