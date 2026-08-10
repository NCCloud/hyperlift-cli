package testapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nccloud/hyperlift-cli/internal/client"
)

// TestListSettlesDueTransitions guards that the list path applies a due pending
// transition, the same way a get does, so `apps list` against the standalone
// mock does not show a transient state forever.
func TestListSettlesDueTransitions(t *testing.T) {
	s := &mockServer{
		apps: SeedApps(),
		pending: map[string]transition{
			// Already due: settleAt is in the past.
			"app_a1b2c3": {status: client.StatusStopped, settleAt: time.Now().Add(-time.Second)},
		},
	}

	w := httptest.NewRecorder()
	s.list(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/hyperlift/applications?take=100&skip=0", nil))

	var page struct {
		Items []client.Application `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode list: %v", err)
	}

	var got *client.Application

	for i := range page.Items {
		if page.Items[i].ID == "app_a1b2c3" {
			got = &page.Items[i]
		}
	}

	if got == nil {
		t.Fatal("seeded app missing from the list")
	}

	if got.Status != client.StatusStopped {
		t.Errorf("status = %s, want the settled %s", got.Status, client.StatusStopped)
	}

	if _, still := s.pending["app_a1b2c3"]; still {
		t.Error("the due transition is still pending after list")
	}
}

// TestListKeepsUndueTransitions checks the counterpart: a transition that is
// not due yet stays pending and the transient state stays visible.
func TestListKeepsUndueTransitions(t *testing.T) {
	s := &mockServer{
		apps: SeedApps(),
		pending: map[string]transition{
			"app_a1b2c3": {status: client.StatusStopped, settleAt: time.Now().Add(time.Hour)},
		},
	}

	w := httptest.NewRecorder()
	s.list(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/hyperlift/applications?take=100&skip=0", nil))

	if _, still := s.pending["app_a1b2c3"]; !still {
		t.Error("an undue transition was settled early")
	}
}
