package docker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wellivea1/noobboard/internal/models"
)

const testUnraidDockerObjectID = "bfcd62805c42d4c95f3ec13f28d7d1c4dcd4913d8c93531268e78c98391577d9:968ab6836d4469c5ab1b37c6d6f0bc078dd53bc3b99239cf1cfcd1a6ee3e1fc4"

func TestUnraidDockerNameAndStatusMapping(t *testing.T) {
	if got := displayName([]string{"/EmbyServer"}); got != "EmbyServer" {
		t.Fatalf("displayName = %q", got)
	}
	if got := appID("binhex-qbittorrentvpn"); got != "binhex-qbittorrentvpn" {
		t.Fatalf("appID = %q", got)
	}
	if got := dockerState("RUNNING"); got != models.DockerRunning {
		t.Fatalf("dockerState = %q", got)
	}
	if got := dockerHealth("Up 5 hours (unhealthy)"); got != models.HealthUnhealthy {
		t.Fatalf("dockerHealth = %q", got)
	}
	if got := currentStatusFromDocker(models.DockerRunning, models.HealthUnhealthy); got != models.StatusDegraded {
		t.Fatalf("currentStatusFromDocker = %q", got)
	}
}

func TestUnraidDockerUsesDocumentedDockerContainersQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Fatalf("x-api-key header = %q", got)
		}
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(body.Query, "dockerContainers") {
			t.Fatalf("query did not use documented dockerContainers field: %s", body.Query)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"dockerContainers":[{"id":"1","names":"/Emby","state":"running","status":"Up 1 hour (healthy)","autoStart":"true","image":"emby/embyserver:latest","labels":{"net.unraid.docker.icon":"https://example.invalid/emby.png"},"webUiUrl":"http://[IP]:[PORT:32400]/web","templatePath":"/boot/config/plugins/dockerMan/templates-user/my-emby.xml"}]}}`))
	}))
	defer server.Close()

	client := NewUnraidLiveClient(server.URL, "test-key")
	client.http = server.Client()
	apps, err := client.Apps(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 {
		t.Fatalf("apps len = %d", len(apps))
	}
	if apps[0].DisplayName != "Emby" || apps[0].CurrentStatus != models.StatusOnline {
		t.Fatalf("unexpected app mapping: %#v", apps[0])
	}
	if apps[0].IconURL != "https://example.invalid/emby.png" || apps[0].IconSource != "docker-label" {
		t.Fatalf("unexpected icon mapping: %#v", apps[0])
	}
	if apps[0].ImageRef != "emby/embyserver:latest" || apps[0].TemplatePath == "" || apps[0].WebURL == "" {
		t.Fatalf("docker metadata was not mapped: %#v", apps[0])
	}
	if apps[0].ContainerID != "container:1" {
		t.Fatalf("container id was not normalized: %#v", apps[0])
	}
}

func TestUnraidDockerPreservesRawGraphQLContainerID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"dockerContainers":[{"id":"` + testUnraidDockerObjectID + `","names":["/EmbyServer"],"state":"exited","status":"Exited (137)","autoStart":false}]}}`))
	}))
	defer server.Close()

	client := NewUnraidLiveClient(server.URL, "test-key")
	client.http = server.Client()
	apps, err := client.Apps(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 {
		t.Fatalf("apps len = %d", len(apps))
	}
	if apps[0].ContainerID != testUnraidDockerObjectID {
		t.Fatalf("raw GraphQL container id was not preserved: %#v", apps[0])
	}
}

func TestUnraidDockerFallsBackToLegacyNestedContainerQuery(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(body.Query, "dockerContainers") {
			_, _ = w.Write([]byte(`{"errors":[{"message":"Cannot query field \"dockerContainers\""}]}`))
			return
		}
		if !strings.Contains(body.Query, "docker") {
			t.Fatalf("fallback query did not request docker: %s", body.Query)
		}
		_, _ = w.Write([]byte(`{"data":{"docker":{"containers":[{"id":"1","names":["/HomeAssistant"],"state":"exited","status":"Exited (1)","autoStart":false}]}}}`))
	}))
	defer server.Close()

	client := NewUnraidLiveClient(server.URL, "test-key")
	client.http = server.Client()
	apps, err := client.Apps(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d", calls)
	}
	if len(apps) != 1 || apps[0].CurrentStatus != models.StatusOffline {
		t.Fatalf("unexpected apps: %#v", apps)
	}
}

func TestDockerIconLabelsAreSanitized(t *testing.T) {
	if got := iconURL(map[string]string{"net.unraid.docker.icon": "https://example.invalid/icon.png"}); got != "https://example.invalid/icon.png" {
		t.Fatalf("valid icon URL = %q", got)
	}
	for _, labels := range []map[string]string{
		{"net.unraid.docker.icon": "javascript:alert(1)"},
		{"net.unraid.docker.icon": "https://user:pass@example.invalid/icon.png"},
		{"net.unraid.docker.icon": "ftp://example.invalid/icon.png"},
	} {
		if got := iconURL(labels); got != "" {
			t.Fatalf("unsafe icon label should be ignored, got %q", got)
		}
		if got := iconSource(labels); got != "" {
			t.Fatalf("unsafe icon source should be empty, got %q", got)
		}
	}
}

func TestUnraidDockerControlUsesGraphQLVariables(t *testing.T) {
	var calls []struct {
		Query     string                 `json:"query"`
		Variables map[string]interface{} `json:"variables"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string                 `json:"query"`
			Variables map[string]interface{} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, body)
		if !strings.Contains(body.Query, "docker") || !strings.Contains(body.Query, "$id: PrefixedID!") {
			t.Fatalf("mutation did not use docker PrefixedID variable: %s", body.Query)
		}
		if body.Variables["id"] != "container:Emby" {
			t.Fatalf("target id = %#v", body.Variables["id"])
		}
		w.Header().Set("Content-Type", "application/json")
		if !strings.Contains(body.Query, "restart(id: $id)") {
			t.Fatalf("restart should use native restart mutation, got: %s", body.Query)
		}
		_, _ = w.Write([]byte(`{"data":{"docker":{"restart":{"id":"container:Emby","state":"running","status":"Restarted"}}}}`))
	}))
	defer server.Close()

	client := NewUnraidLiveClient(server.URL, "test-key")
	client.http = server.Client()
	result, err := client.ControlContainer(t.Context(), models.AppStatus{AppID: "emby", ContainerName: "Emby"}, ActionRestart)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	if result.Action != ActionRestart || result.DockerState != models.DockerRunning || result.Status != "Restarted" {
		t.Fatalf("unexpected control result: %#v", result)
	}
}

func TestUnraidDockerControlPrefersRawGraphQLContainerID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string                 `json:"query"`
			Variables map[string]interface{} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Variables["id"] != testUnraidDockerObjectID {
			t.Fatalf("target id = %#v, want raw Unraid Docker object id", body.Variables["id"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"docker":{"start":{"id":"` + testUnraidDockerObjectID + `","state":"running","status":"Up 1 second"}}}}`))
	}))
	defer server.Close()

	client := NewUnraidLiveClient(server.URL, "test-key")
	client.http = server.Client()
	result, err := client.ControlContainer(t.Context(), models.AppStatus{AppID: "emby", ContainerID: testUnraidDockerObjectID, ContainerName: "EmbyServer"}, ActionStart)
	if err != nil {
		t.Fatal(err)
	}
	if result.ContainerID != testUnraidDockerObjectID || result.Action != ActionStart {
		t.Fatalf("unexpected control result: %#v", result)
	}
}

func TestUnraidDockerControlTimeoutAfterSendReturnsUnconfirmedResult(t *testing.T) {
	client := NewUnraidLiveClient("http://192.168.0.214", "test-key")
	client.http = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New(`Post "http://192.168.0.214/graphql": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`)
	})}

	result, err := client.ControlContainer(t.Context(), models.AppStatus{
		AppID:         "emby",
		ContainerID:   testUnraidDockerObjectID,
		ContainerName: "EmbyServer",
	}, ActionStop)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != ActionStop || result.AppID != "emby" || result.ContainerID != testUnraidDockerObjectID {
		t.Fatalf("timeout result = %#v", result)
	}
	if !strings.Contains(result.Status, "did not confirm before timeout") {
		t.Fatalf("timeout result status = %q", result.Status)
	}
}

func TestUnraidDockerRestartFallsBackToStopStartWhenRestartUnsupported(t *testing.T) {
	var operations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string                 `json:"query"`
			Variables map[string]interface{} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Variables["id"] != "container:Emby" {
			t.Fatalf("target id = %#v", body.Variables["id"])
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(body.Query, "restart(id: $id)"):
			operations = append(operations, "restart")
			_, _ = w.Write([]byte(`{"errors":[{"message":"Cannot query field \"restart\" on type \"Docker\"."}]}`))
		case strings.Contains(body.Query, "stop(id: $id)"):
			operations = append(operations, "stop")
			_, _ = w.Write([]byte(`{"data":{"docker":{"stop":{"id":"container:Emby","state":"exited","status":"Exited"}}}}`))
		case strings.Contains(body.Query, "start(id: $id)"):
			operations = append(operations, "start")
			_, _ = w.Write([]byte(`{"data":{"docker":{"start":{"id":"container:Emby","state":"running","status":"Up 1 second"}}}}`))
		default:
			t.Fatalf("unexpected mutation: %s", body.Query)
		}
	}))
	defer server.Close()

	client := NewUnraidLiveClient(server.URL, "test-key")
	client.http = server.Client()
	result, err := client.ControlContainer(t.Context(), models.AppStatus{AppID: "emby", ContainerName: "Emby"}, ActionRestart)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"restart", "stop", "start"}
	if strings.Join(operations, ",") != strings.Join(want, ",") {
		t.Fatalf("operations = %#v", operations)
	}
	if result.Action != ActionRestart || result.DockerState != models.DockerRunning {
		t.Fatalf("unexpected control result: %#v", result)
	}
}

func TestUnraidDockerRestartFallbackReportsPossibleStoppedContainer(t *testing.T) {
	var startCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string                 `json:"query"`
			Variables map[string]interface{} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Variables["id"] != "container:Emby" {
			t.Fatalf("target id = %#v", body.Variables["id"])
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(body.Query, "restart(id: $id)"):
			_, _ = w.Write([]byte(`{"errors":[{"message":"Cannot query field \"restart\" on type \"Docker\"."}]}`))
		case strings.Contains(body.Query, "stop(id: $id)"):
			_, _ = w.Write([]byte(`{"data":{"docker":{"stop":{"id":"container:Emby","state":"exited","status":"Exited"}}}}`))
		case strings.Contains(body.Query, "start(id: $id)"):
			startCalls++
			_, _ = w.Write([]byte(`{"errors":[{"message":"container failed to start"}]}`))
		default:
			t.Fatalf("unexpected mutation: %s", body.Query)
		}
	}))
	defer server.Close()

	client := NewUnraidLiveClient(server.URL, "test-key")
	client.http = server.Client()
	_, err := client.ControlContainer(t.Context(), models.AppStatus{AppID: "emby", ContainerName: "Emby"}, ActionRestart)
	if err == nil || !strings.Contains(err.Error(), "container may still be stopped") {
		t.Fatalf("expected possible stopped-container error, got %v", err)
	}
	if startCalls != 2 {
		t.Fatalf("start calls = %d", startCalls)
	}
}

func TestUnraidDockerControlRequiresStableContainerTarget(t *testing.T) {
	client := NewUnraidLiveClient("http://example.invalid", "test-key")
	_, err := client.ControlContainer(t.Context(), models.AppStatus{AppID: "emby", DisplayName: "Emby"}, ActionStop)
	if err == nil || !strings.Contains(err.Error(), "docker container id or name is required") {
		t.Fatalf("expected stable target error, got %v", err)
	}
}

func TestUnraidDockerControlRejectsNonContainerPrefixedTarget(t *testing.T) {
	client := NewUnraidLiveClient("http://example.invalid", "test-key")
	_, err := client.ControlContainer(t.Context(), models.AppStatus{AppID: "emby", ContainerID: "array:md1"}, ActionStop)
	if err == nil || !strings.Contains(err.Error(), "docker container id or name is required") {
		t.Fatalf("expected non-container target rejection, got %v", err)
	}
}

func TestUnraidDockerLogsUseGraphQLVariablesAndParseLines(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string                 `json:"query"`
			Variables map[string]interface{} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(body.Query, "logs(id: $id)") || !strings.Contains(body.Query, "$id: PrefixedID!") {
			t.Fatalf("log query did not use docker PrefixedID variable: %s", body.Query)
		}
		if body.Variables["id"] != "container:Emby" {
			t.Fatalf("target id = %#v", body.Variables["id"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"docker":{"logs":{"lines":["first","second"]}}}}`))
	}))
	defer server.Close()

	client := NewUnraidLiveClient(server.URL, "test-key")
	client.http = server.Client()
	lines, err := client.Logs(t.Context(), models.AppStatus{AppID: "emby", ContainerName: "Emby"}, LogOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0].Source != "Emby" || lines[0].Line != "second" {
		t.Fatalf("unexpected logs: %#v", lines)
	}
}

func TestUnraidDockerLogsFallbackAcrossFields(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(body.Query, "lines") {
			_, _ = w.Write([]byte(`{"errors":[{"message":"Cannot query field \"lines\" on type \"DockerContainerLogs\"."}]}`))
			return
		}
		if !strings.Contains(body.Query, "logs") {
			t.Fatalf("fallback did not request logs field: %s", body.Query)
		}
		_, _ = w.Write([]byte(`{"data":{"docker":{"logs":{"logs":"alpha\nbeta"}}}}`))
	}))
	defer server.Close()

	client := NewUnraidLiveClient(server.URL, "test-key")
	client.http = server.Client()
	lines, err := client.Logs(t.Context(), models.AppStatus{AppID: "emby", ContainerID: "container:Emby"}, LogOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d", calls)
	}
	if len(lines) != 2 || lines[0].Line != "alpha" || lines[1].Line != "beta" {
		t.Fatalf("unexpected logs: %#v", lines)
	}
}

func TestSSHAppsFromDockerCLIOutput(t *testing.T) {
	apps := appsFromCLIContainers([]cliContainer{{
		ID:     "abcdef",
		Names:  "EmbyServer",
		Image:  "emby/embyserver:latest",
		State:  "running",
		Status: "Up 24 hours",
		Labels: "net.unraid.docker.icon=https://example.invalid/emby.png,ignored",
	}})
	if len(apps) != 1 {
		t.Fatalf("apps len = %d", len(apps))
	}
	if apps[0].DataSource != "unraid-ssh-docker" {
		t.Fatalf("data source = %q", apps[0].DataSource)
	}
	if apps[0].CurrentStatus != models.StatusOnline || apps[0].DockerState != models.DockerRunning {
		t.Fatalf("unexpected status mapping: %#v", apps[0])
	}
	if apps[0].IconURL != "https://example.invalid/emby.png" {
		t.Fatalf("icon label was not parsed: %#v", apps[0])
	}
}

func TestLargestListClientPrefersFallbackWhenItSeesMoreApps(t *testing.T) {
	primary := stubDockerClient{apps: []models.AppStatus{{AppID: "one", DataSource: "unraid-docker"}}}
	fallback := stubDockerClient{apps: []models.AppStatus{
		{AppID: "one", DataSource: "unraid-ssh-docker"},
		{AppID: "two", DataSource: "unraid-ssh-docker"},
	}}
	apps, err := NewLargestListClient(primary, fallback).Apps(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 2 || apps[1].AppID != "two" {
		t.Fatalf("fallback list was not preferred: %#v", apps)
	}
}

func TestLargestListClientDoesNotFallbackForNonFallbackableControlError(t *testing.T) {
	primaryErr := errors.New("unraid docker graphql error: Forbidden resource")
	primary := stubDockerClient{err: primaryErr}
	fallback := stubDockerClient{result: ControlResult{Status: "fallback accepted"}}
	_, err := NewLargestListClient(primary, fallback).ControlContainer(t.Context(), models.AppStatus{AppID: "emby", ContainerName: "Emby"}, ActionStop)
	if !errors.Is(err, primaryErr) {
		t.Fatalf("expected primary error, got %v", err)
	}
}

func TestLargestListClientFallbackForFallbackableControlError(t *testing.T) {
	primary := stubDockerClient{err: markFallbackable(errors.New("unraid docker graphql returned 503"))}
	fallback := stubDockerClient{result: ControlResult{Status: "fallback accepted"}}
	result, err := NewLargestListClient(primary, fallback).ControlContainer(t.Context(), models.AppStatus{AppID: "emby", ContainerName: "Emby"}, ActionStop)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "fallback accepted" {
		t.Fatalf("expected fallback result, got %#v", result)
	}
}

type stubDockerClient struct {
	apps   []models.AppStatus
	result ControlResult
	err    error
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func (c stubDockerClient) Apps(context.Context) ([]models.AppStatus, error) {
	return c.apps, c.err
}

func (c stubDockerClient) ControlContainer(context.Context, models.AppStatus, ContainerAction) (ControlResult, error) {
	return c.result, c.err
}

func (c stubDockerClient) Logs(context.Context, models.AppStatus, LogOptions) ([]models.LogLine, error) {
	return nil, c.err
}
