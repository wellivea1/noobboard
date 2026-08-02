package probes

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/wellivea1/noobboard/internal/models"
)

type LiveConfig struct {
	InternetURL  string
	DNSHost      string
	RouterTarget string
	NASTarget    string
	Timeout      time.Duration
}

type LiveClient struct {
	cfg      LiveConfig
	http     *http.Client
	resolver *net.Resolver
}

func NewLiveClient(cfg LiveConfig) LiveClient {
	cfg.InternetURL = strings.TrimSpace(strings.TrimRight(cfg.InternetURL, "/"))
	cfg.DNSHost = strings.TrimSpace(cfg.DNSHost)
	cfg.RouterTarget = strings.TrimSpace(strings.TrimRight(cfg.RouterTarget, "/"))
	cfg.NASTarget = strings.TrimSpace(strings.TrimRight(cfg.NASTarget, "/"))
	if cfg.Timeout <= 0 {
		cfg.Timeout = 3 * time.Second
	}
	return LiveClient{
		cfg:      cfg,
		http:     &http.Client{Timeout: cfg.Timeout},
		resolver: net.DefaultResolver,
	}
}

func (c LiveClient) Status(ctx context.Context) (models.InfrastructureStatus, error) {
	if c.cfg.InternetURL == "" && c.cfg.DNSHost == "" && c.cfg.RouterTarget == "" && c.cfg.NASTarget == "" {
		return models.InfrastructureStatus{}, errors.New("live network probe targets are not configured")
	}
	infra := models.InfrastructureStatus{
		LastCheckedAt: time.Now().UTC(),
		SourceHealth:  models.SourceHealth{},
	}
	var parts []string
	// Each probe is timed as well as tested. The timing costs nothing extra —
	// it is the same request — and it is the only thing that can answer "is the
	// internet slow", which reachability booleans never could.
	record := func(subject string, ok bool, elapsed time.Duration) {
		infra.ProbeLatencies = append(infra.ProbeLatencies, models.ProbeLatency{
			Subject:   subject,
			OK:        ok,
			LatencyMS: elapsed.Milliseconds(),
		})
		parts = append(parts, healthPart(subject, ok))
	}
	if c.cfg.InternetURL != "" {
		ok, elapsed := timed(func() bool { return c.httpReachable(ctx, c.cfg.InternetURL) })
		infra.InternetReachable = ok
		record(ProbeInternet, ok, elapsed)
	} else {
		parts = append(parts, "internet skipped")
	}
	if c.cfg.DNSHost != "" {
		ok, elapsed := timed(func() bool { return c.dnsReachable(ctx, c.cfg.DNSHost) })
		infra.DNSOK = ok
		record(ProbeDNS, ok, elapsed)
	} else {
		parts = append(parts, "dns skipped")
	}
	if c.cfg.RouterTarget != "" {
		ok, elapsed := timed(func() bool { return c.targetReachable(ctx, c.cfg.RouterTarget) })
		infra.RouterReachable = ok
		record(ProbeRouter, ok, elapsed)
	} else {
		parts = append(parts, "router skipped")
	}
	if c.cfg.NASTarget != "" {
		ok, elapsed := timed(func() bool { return c.targetReachable(ctx, c.cfg.NASTarget) })
		infra.NASReachable = ok
		record(ProbeNAS, ok, elapsed)
	} else {
		parts = append(parts, "nas skipped")
	}
	infra.SourceHealth.Probes = strings.Join(parts, "; ")
	return infra, nil
}

func (c LiveClient) httpReachable(ctx context.Context, target string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return false
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 500
}

func (c LiveClient) dnsReachable(ctx context.Context, host string) bool {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	addrs, err := c.resolver.LookupHost(ctx, host)
	return err == nil && len(addrs) > 0
}

func (c LiveClient) targetReachable(ctx context.Context, target string) bool {
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		parsed, err := url.Parse(target)
		if err != nil || parsed.Host == "" {
			return false
		}
		host := parsed.Hostname()
		port := parsed.Port()
		if port == "" && parsed.Scheme == "http" {
			port = "80"
		}
		if port == "" {
			port = "443"
		}
		return c.tcpReachable(ctx, net.JoinHostPort(host, port))
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		host = target
		port = "443"
	}
	return c.tcpReachable(ctx, net.JoinHostPort(host, port))
}

func (c LiveClient) tcpReachable(ctx context.Context, address string) bool {
	dialer := net.Dialer{Timeout: c.cfg.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// Probe subject names. Shared with the server's rolling window and the rules,
// so a rename cannot silently orphan a baseline.
const (
	ProbeInternet = "internet"
	ProbeDNS      = "dns"
	ProbeRouter   = "router"
	ProbeNAS      = "nas"
)

func timed(check func() bool) (bool, time.Duration) {
	start := time.Now()
	ok := check()
	return ok, time.Since(start)
}

func healthPart(name string, ok bool) string {
	if ok {
		return fmt.Sprintf("%s ok", name)
	}
	return fmt.Sprintf("%s failed", name)
}

func HostFromURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return strings.TrimSpace(raw)
	}
	return parsed.Host
}
