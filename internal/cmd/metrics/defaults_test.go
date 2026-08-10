package metrics

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nccloud/hyperlift-cli/internal/client"
	"github.com/nccloud/hyperlift-cli/internal/testapi"
)

// defaultMetrics holds hand-typed selector tokens with no compile-time link to
// the contract. The mock rejects an unknown selector, so this test catches a
// typo before customers do.
func TestDefaultMetricsAreKnownToTheContract(t *testing.T) {
	srv := httptest.NewServer(testapi.Server())
	defer srv.Close()

	c := client.New(client.Options{BaseURL: srv.URL, APIKey: "k", APISecret: "s"})

	m, err := c.Metrics(context.Background(), "app_a1b2c3", client.MetricsQuery{
		Start:    time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		End:      time.Date(2026, 1, 15, 1, 0, 0, 0, time.UTC),
		Interval: "5m",
		Metrics:  defaultMetrics,
	})
	if err != nil {
		t.Fatalf("default metrics rejected by the contract mock: %v", err)
	}

	if len(m.Series) != len(defaultMetrics) {
		t.Fatalf("series = %d, want %d", len(m.Series), len(defaultMetrics))
	}
}
