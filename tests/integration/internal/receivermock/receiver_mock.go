package receivermock

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	http_helper "github.com/gruntwork-io/terratest/modules/http-helper"

	"github.com/SumoLogic/sumologic-kubernetes-collection/tests/integration/internal/k8s"
)

// Mapping of metric names to the number of times the metric was observed
type MetricCounts map[string]int

// A HTTP client for the receiver-mock API
type ReceiverMockClient struct {
	baseUrl   url.URL
	tlsConfig tls.Config
}

func NewClient(t *testing.T, baseUrl url.URL) *ReceiverMockClient {
	return &ReceiverMockClient{baseUrl: baseUrl, tlsConfig: tls.Config{}}
}

// NewClientWithK8sTunnel creates a client for receiver-mock.
// It return the client itself and a tunnel teardown func which should be called
// by the caller when they're done with it.
func NewClientWithK8sTunnel(
	ctx context.Context,
	t *testing.T,
) (*ReceiverMockClient, func()) {
	tunnel := k8s.TunnelForReceiverMock(ctx, t)
	baseUrl := url.URL{
		Scheme: "http",
		Host:   tunnel.Endpoint(),
		Path:   "/",
	}

	return &ReceiverMockClient{
			baseUrl:   baseUrl,
			tlsConfig: tls.Config{},
		}, func() {
			tunnel.Close()
		}
}

func (client *ReceiverMockClient) GetMetricCounts(t *testing.T) (MetricCounts, error) {
	path, err := url.Parse("metrics-list")
	if err != nil {
		t.Fatal(err)
	}
	url := client.baseUrl.ResolveReference(path)

	statusCode, body := http_helper.HttpGet(
		t,
		url.String(),
		&client.tlsConfig,
	)
	if statusCode != 200 {
		return nil, fmt.Errorf("received status code %d in response to receiver request", statusCode)
	}
	metricCounts, err := parseMetricList(body)
	if err != nil {
		t.Fatal(err)
	}
	return metricCounts, nil
}

type MetricSample struct {
	Metric    string  `json:"metric,omitempty"`
	Value     float64 `json:"value,omitempty"`
	Labels    Labels  `json:"labels,omitempty"`
	Timestamp uint64  `json:"timestamp,omitempty"`
}

type MetricsSamplesByTime []MetricSample

func (m MetricsSamplesByTime) Len() int           { return len(m) }
func (m MetricsSamplesByTime) Swap(i, j int)      { m[i], m[j] = m[j], m[i] }
func (m MetricsSamplesByTime) Less(i, j int) bool { return m[i].Timestamp > m[j].Timestamp }

type MetadataFilters map[string]string

func (client *ReceiverMockClient) GetMetricsSamples(
	metadataFilters MetadataFilters,
) ([]MetricSample, error) {
	path, err := url.Parse("metrics-samples")
	if err != nil {
		return nil, fmt.Errorf("failed parsing metrics-samples url: %w", err)
	}
	u := client.baseUrl.ResolveReference(path)

	q := u.Query()
	for k, v := range metadataFilters {
		q.Add(k, v)
	}
	u.RawQuery = q.Encode()

	resp, err := http.Get(u.String())
	if err != nil {
		return nil, fmt.Errorf("failed fetching %s, err: %w", u, err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf(
			"received status code %d in response to receiver request at %q",
			resp.StatusCode, u,
		)
	}

	var metricsSamples []MetricSample
	if err := json.NewDecoder(resp.Body).Decode(&metricsSamples); err != nil {
		return nil, err
	}
	return metricsSamples, nil
}

// parse metrics list returned by /metrics-list
// https://github.com/SumoLogic/sumologic-kubernetes-tools/tree/main/src/rust/receiver-mock#statistics
func parseMetricList(rawMetricsValues string) (map[string]int, error) {
	metricNameToCount := make(map[string]int)
	lines := strings.Split(rawMetricsValues, "\n")
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		// the last colon of the line is the split point
		splitIndex := strings.LastIndex(line, ":")
		if splitIndex == -1 || splitIndex == 0 {
			return nil, fmt.Errorf("failed to parse metrics list line: %q", line)
		}
		metricName := line[:splitIndex]
		metricCountString := strings.TrimSpace(line[splitIndex+1:])
		metricCount, err := strconv.Atoi(metricCountString)
		if err != nil {
			return nil, err
		}
		metricNameToCount[metricName] = metricCount
	}
	return metricNameToCount, nil
}
