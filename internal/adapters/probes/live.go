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

	"github.com/wellivea1/server-status/internal/models"
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
	if c.cfg.InternetURL != "" {
		infra.InternetReachable = c.httpReachable(ctx, c.cfg.InternetURL)
		parts = append(parts, healthPart("internet", infra.InternetReachable))
	} else {
		parts = append(parts, "internet skipped")
	}
	if c.cfg.DNSHost != "" {
		infra.DNSOK = c.dnsReachable(ctx, c.cfg.DNSHost)
		parts = append(parts, healthPart("dns", infra.DNSOK))
	} else {
		parts = append(parts, "dns skipped")
	}
	if c.cfg.RouterTarget != "" {
		infra.RouterReachable = c.targetReachable(ctx, c.cfg.RouterTarget)
		parts = append(parts, healthPart("router", infra.RouterReachable))
	} else {
		parts = append(parts, "router skipped")
	}
	if c.cfg.NASTarget != "" {
		infra.NASReachable = c.targetReachable(ctx, c.cfg.NASTarget)
		parts = append(parts, healthPart("nas", infra.NASReachable))
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
