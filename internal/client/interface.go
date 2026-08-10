package client

import "context"

// Client is the factory wiring surface. Feature packages do not depend on it:
// each one declares a narrow interface of the methods it calls.
type Client interface {
	// Probe verifies the credentials and connectivity. login and whoami use it.
	Probe(ctx context.Context) error

	// AppsList returns one page of the caller's applications and the total
	// count. Paging is by offset: take items after skip.
	AppsList(ctx context.Context, take, skip int) (apps []Application, total int, err error)
	// AppsGet returns a single application by id.
	AppsGet(ctx context.Context, id string) (*Application, error)

	// The lifecycle operations return an OpRef, the id of the mutated
	// application. Start and Stop use the scale endpoint: 1 runs, 0 stops.
	AppsBuild(ctx context.Context, id string) (*OpRef, error)
	AppsStart(ctx context.Context, id string) (*OpRef, error)
	AppsStop(ctx context.Context, id string) (*OpRef, error)
	AppsRestart(ctx context.Context, id string) (*OpRef, error)

	// EnvGet returns the full environment map.
	EnvGet(ctx context.Context, id string) (map[string]string, error)
	// EnvUpdate replaces the complete environment map.
	EnvUpdate(ctx context.Context, id string, env map[string]string) error

	// Metrics returns time-series metrics for an application.
	Metrics(ctx context.Context, id string, q MetricsQuery) (*Metrics, error)

	// Logs reads one bounded page of runtime log lines. To follow the log, poll
	// with the returned cursor: the External API has no streaming.
	Logs(ctx context.Context, req LogsRequest) (*LogsPage, error)

	// BuildLogs reads one bounded page of build log lines. It uses the same
	// paging contract as Logs.
	BuildLogs(ctx context.Context, req LogsRequest) (*LogsPage, error)
}
