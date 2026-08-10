// Package clienttest holds a configurable test double for the client API. Unit
// tests use it to script one call at a time.
package clienttest

import (
	"context"

	"github.com/nccloud/hyperlift-cli/internal/client"
)

// Fake is a configurable test double that implements client.Client. Set the
// function fields you need. An unset function returns a zero value or nil.
type Fake struct {
	ProbeFunc       func(ctx context.Context) error
	AppsListFunc    func(ctx context.Context, take, skip int) ([]client.Application, int, error)
	AppsGetFunc     func(ctx context.Context, id string) (*client.Application, error)
	AppsBuildFunc   func(ctx context.Context, id string) (*client.OpRef, error)
	AppsStartFunc   func(ctx context.Context, id string) (*client.OpRef, error)
	AppsStopFunc    func(ctx context.Context, id string) (*client.OpRef, error)
	AppsRestartFunc func(ctx context.Context, id string) (*client.OpRef, error)
	EnvGetFunc      func(ctx context.Context, id string) (map[string]string, error)
	EnvUpdateFunc   func(ctx context.Context, id string, env map[string]string) error
	MetricsFunc     func(ctx context.Context, id string, q client.MetricsQuery) (*client.Metrics, error)
	LogsFunc        func(ctx context.Context, req client.LogsRequest) (*client.LogsPage, error)
	BuildLogsFunc   func(ctx context.Context, req client.LogsRequest) (*client.LogsPage, error)
}

var _ client.Client = (*Fake)(nil)

func (m *Fake) Probe(ctx context.Context) error {
	if m.ProbeFunc != nil {
		return m.ProbeFunc(ctx)
	}

	return nil
}

func (m *Fake) AppsList(ctx context.Context, take, skip int) ([]client.Application, int, error) {
	if m.AppsListFunc != nil {
		return m.AppsListFunc(ctx, take, skip)
	}

	return nil, 0, nil
}

func (m *Fake) AppsGet(ctx context.Context, id string) (*client.Application, error) {
	if m.AppsGetFunc != nil {
		return m.AppsGetFunc(ctx, id)
	}

	return nil, nil
}

func (m *Fake) AppsBuild(ctx context.Context, id string) (*client.OpRef, error) {
	if m.AppsBuildFunc != nil {
		return m.AppsBuildFunc(ctx, id)
	}

	return &client.OpRef{ID: id}, nil
}

func (m *Fake) AppsStart(ctx context.Context, id string) (*client.OpRef, error) {
	if m.AppsStartFunc != nil {
		return m.AppsStartFunc(ctx, id)
	}

	return &client.OpRef{ID: id}, nil
}

func (m *Fake) AppsStop(ctx context.Context, id string) (*client.OpRef, error) {
	if m.AppsStopFunc != nil {
		return m.AppsStopFunc(ctx, id)
	}

	return &client.OpRef{ID: id}, nil
}

func (m *Fake) AppsRestart(ctx context.Context, id string) (*client.OpRef, error) {
	if m.AppsRestartFunc != nil {
		return m.AppsRestartFunc(ctx, id)
	}

	return &client.OpRef{ID: id}, nil
}

func (m *Fake) EnvGet(ctx context.Context, id string) (map[string]string, error) {
	if m.EnvGetFunc != nil {
		return m.EnvGetFunc(ctx, id)
	}

	return map[string]string{}, nil
}

func (m *Fake) EnvUpdate(ctx context.Context, id string, env map[string]string) error {
	if m.EnvUpdateFunc != nil {
		return m.EnvUpdateFunc(ctx, id, env)
	}

	return nil
}

func (m *Fake) Metrics(ctx context.Context, id string, q client.MetricsQuery) (*client.Metrics, error) {
	if m.MetricsFunc != nil {
		return m.MetricsFunc(ctx, id, q)
	}

	return &client.Metrics{}, nil
}

func (m *Fake) Logs(ctx context.Context, req client.LogsRequest) (*client.LogsPage, error) {
	if m.LogsFunc != nil {
		return m.LogsFunc(ctx, req)
	}

	return &client.LogsPage{Finished: true}, nil
}

func (m *Fake) BuildLogs(ctx context.Context, req client.LogsRequest) (*client.LogsPage, error) {
	if m.BuildLogsFunc != nil {
		return m.BuildLogsFunc(ctx, req)
	}

	return &client.LogsPage{Finished: true}, nil
}
