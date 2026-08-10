// Package client defines the Hyperlift API contract: the data types, the Client
// interface, the HTTP implementation, and the transport helpers.
package client

import "time"

// AppStatus is an application lifecycle status, in the wire values the Hyperlift
// API returns.
type AppStatus string

const (
	StatusCreating   AppStatus = "creating"
	StatusCreated    AppStatus = "created"
	StatusDeploying  AppStatus = "deploying"
	StatusInStart    AppStatus = "instart"
	StatusRunning    AppStatus = "running"
	StatusInStop     AppStatus = "instop"
	StatusStopped    AppStatus = "stopped"
	StatusRestarting AppStatus = "restarting"
	StatusResetting  AppStatus = "resetting"
	StatusDeleting   AppStatus = "deleting"
	StatusFailure    AppStatus = "failure"
)

// BuildStatus is an application build status, spelled as the public API spells
// it.
type BuildStatus string

const (
	BuildStatusBuilding BuildStatus = "building"
	BuildStatusBuilt    BuildStatus = "built"
	BuildStatusFailed   BuildStatus = "failed"
	BuildStatusNone     BuildStatus = "none"
)

// Application is the external, sanitized view of a Hyperlift container
// application. It mirrors the contract's ExternalApplicationResponse. Every
// field the contract declares required-but-nullable is a pointer: --json
// re-marshals this struct, so a wire null must stay null, not a zero value.
type Application struct {
	ID          string      `json:"id"`
	Status      AppStatus   `json:"status"`
	BuildStatus BuildStatus `json:"buildStatus"`
	Plan        string      `json:"plan"`
	Domain      *string     `json:"domain"` // single, nullable
	// Scale is 0 or 1. It is null while the desired scale is unknown, for
	// example during first provisioning.
	Scale  *int    `json:"scale"`
	Branch *string `json:"branch"`
	// GithubInstallationID is a number on the wire. The contract scalar
	// githubInstallationId extends int32; int64 decoding is the safe choice.
	GithubInstallationID     *int64     `json:"githubInstallationId"`
	GithubRepositoryFullName *string    `json:"githubRepositoryFullName"`
	DockerfilePath           *string    `json:"dockerfilePath"`
	AutomaticBuildEnabled    *bool      `json:"automaticBuildEnabled"`
	CreatedAt                time.Time  `json:"createdAt"`
	UpdatedAt                *time.Time `json:"updatedAt"`
}

// OpRef is the ApplicationMutationResponse ({id}) that build, restart and scale
// return. To follow the progress, poll the application status and build_status.
type OpRef struct {
	ID string `json:"id"`
}

// scaleRequest is the ScaleApplicationRequest body of PUT /{id}/scale. 0 stops
// the application; 1 runs a single instance.
type scaleRequest struct {
	Scale int `json:"scale"`
}

// MetricsQuery is the metrics request over the inclusive range [Start,End]. The
// client sends Metrics comma-joined.
type MetricsQuery struct {
	Start    time.Time
	End      time.Time
	Interval string
	Metrics  []string
}

// Metrics is the metrics response: one series per requested metric, in request
// order.
type Metrics struct {
	Series []MetricSeries `json:"metrics"`
}

// MetricSeries holds the samples of one metric over the queried window. Quota is
// set only where the plan defines a limit. Samples are dense: every series shares
// one timestamp grid and a bucket with no observation reports 0, so a zero is
// indistinguishable from no data.
type MetricSeries struct {
	Name    string         `json:"name"`
	Unit    string         `json:"unit"`
	Quota   *float64       `json:"quota,omitempty"`
	Samples []MetricSample `json:"samples"`
}

// MetricSample is one bucket of a series.
type MetricSample struct {
	Timestamp string  `json:"timestamp"`
	Value     float64 `json:"value"`
}

// LogEntry is one log line from the logs endpoint.
type LogEntry struct {
	Message   string `json:"message"`
	Timestamp string `json:"timestamp,omitempty"`
}

// LogsRequest is one page request to the logs or build-logs endpoint. Cursor
// resumes after the last line already seen; Take limits the lines per page.
type LogsRequest struct {
	ID     string
	Cursor string
	Take   int
}

// LogsPage is one bounded page of log lines, oldest first. Cursor resumes after
// the last line of this page. Finished means no more lines will ever come, so a
// poller can stop.
type LogsPage struct {
	Items    []LogEntry `json:"items"`
	Cursor   string     `json:"cursor,omitempty"`
	Finished bool       `json:"finished"`
}

// envEnvelope is the wire shape of the environment variables. An update PUTs
// only `items`, which replaces the whole map; the read response also carries an
// `id`, which the client ignores.
type envEnvelope struct {
	Items map[string]string `json:"items"`
}
