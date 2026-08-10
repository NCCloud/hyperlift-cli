package testapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
)

// specFile is a pinned copy of the published Spaceship External API OpenAPI
// document; see testdata/README.md. It is this package's oracle, because the
// mock must not be its own contract.
const specFile = "testdata/spaceship-public-api.yaml"

// specPrefix is the namespace the mock stands in for. A spec path carries the
// /v1 that the client's base URL supplies, so the mock serves it without /v1.
const specPrefix = "/v1/hyperlift/"

// scenario is one call. It names the spec operation the call must satisfy, and
// holds the request to send through the mock. The test validates the response
// against the schema of the returned status, so an error case is only a scenario
// with a non-200 wantStatus.
type scenario struct {
	name       string
	specPath   string // path key in the spec
	method     string
	pathParams map[string]string // as specPath declares them
	path       string            // specPath with the parameters filled in, without /v1
	query      string
	body       string
	wantStatus int  // zero means 200
	badRequest bool // the spec must reject this request too, which is why the mock 422s
}

// seededID is the mock's seeded application. Every per-application scenario uses
// it.
const seededID = "app_a1b2c3"

func seededParams() map[string]string { return map[string]string{"id": seededID} }

var scenarios = []scenario{
	{
		name:     "apps list",
		specPath: "/v1/hyperlift/applications",
		method:   http.MethodGet,
		path:     "/hyperlift/applications",
		query:    "?take=2&skip=0",
	},
	{
		name:       "apps get",
		specPath:   "/v1/hyperlift/applications/{id}",
		method:     http.MethodGet,
		pathParams: seededParams(),
		path:       "/hyperlift/applications/" + seededID,
	},
	{
		name:       "build",
		specPath:   "/v1/hyperlift/applications/{id}/build",
		method:     http.MethodPost,
		pathParams: seededParams(),
		path:       "/hyperlift/applications/" + seededID + "/build",
	},
	{
		name:       "restart",
		specPath:   "/v1/hyperlift/applications/{id}/restart",
		method:     http.MethodPost,
		pathParams: seededParams(),
		path:       "/hyperlift/applications/" + seededID + "/restart",
	},
	{
		name:       "scale start",
		specPath:   "/v1/hyperlift/applications/{id}/scale",
		method:     http.MethodPut,
		pathParams: seededParams(),
		path:       "/hyperlift/applications/" + seededID + "/scale",
		body:       `{"scale":1}`,
	},
	{
		name:       "scale stop",
		specPath:   "/v1/hyperlift/applications/{id}/scale",
		method:     http.MethodPut,
		pathParams: seededParams(),
		path:       "/hyperlift/applications/" + seededID + "/scale",
		body:       `{"scale":0}`,
	},
	{
		name:       "environment get",
		specPath:   "/v1/hyperlift/applications/{id}/environment",
		method:     http.MethodGet,
		pathParams: seededParams(),
		path:       "/hyperlift/applications/" + seededID + "/environment",
	},
	{
		name:       "environment update",
		specPath:   "/v1/hyperlift/applications/{id}/environment",
		method:     http.MethodPut,
		pathParams: seededParams(),
		path:       "/hyperlift/applications/" + seededID + "/environment",
		body:       `{"items":{"NODE_ENV":"production","PORT":"8080"}}`,
	},
	{
		name:       "metrics",
		specPath:   "/v1/hyperlift/applications/{id}/metrics",
		method:     http.MethodGet,
		pathParams: seededParams(),
		path:       "/hyperlift/applications/" + seededID + "/metrics",
		// One metric per unit the contract lists, so the enum checks every unit
		// the mock can write.
		query: "?startDate=2026-06-01T12:00:00.000Z&endDate=2026-06-01T13:00:00.000Z" +
			"&interval=5m&metrics=memoryUsageBytes,cpuUsagePercentage,networkReceiveRateBytes," +
			"persistentStorageUsedMebibytes",
	},
	{
		name:       "logs",
		specPath:   "/v1/hyperlift/applications/{id}/logs",
		method:     http.MethodGet,
		pathParams: seededParams(),
		path:       "/hyperlift/applications/" + seededID + "/logs",
		query:      "?take=2",
	},
	{
		name:       "build logs",
		specPath:   "/v1/hyperlift/applications/{id}/build-logs",
		method:     http.MethodGet,
		pathParams: seededParams(),
		path:       "/hyperlift/applications/" + seededID + "/build-logs",
		query:      "?take=2",
	},
	// An error response is part of the contract too.
	{
		name:       "apps get 429",
		specPath:   "/v1/hyperlift/applications/{id}",
		method:     http.MethodGet,
		pathParams: map[string]string{"id": rateLimitID},
		path:       "/hyperlift/applications/" + rateLimitID,
		wantStatus: http.StatusTooManyRequests,
	},
	{
		name:       "apps get 404",
		specPath:   "/v1/hyperlift/applications/{id}",
		method:     http.MethodGet,
		pathParams: map[string]string{"id": "app_missing"},
		path:       "/hyperlift/applications/app_missing",
		wantStatus: http.StatusNotFound,
	},
	{
		name:       "apps list 422",
		specPath:   "/v1/hyperlift/applications",
		method:     http.MethodGet,
		path:       "/hyperlift/applications",
		query:      "?take=101&skip=0",
		wantStatus: http.StatusUnprocessableEntity,
		badRequest: true,
	},
	{
		name:       "environment update 422 invalid name",
		specPath:   "/v1/hyperlift/applications/{id}/environment",
		method:     http.MethodPut,
		pathParams: seededParams(),
		path:       "/hyperlift/applications/" + seededID + "/environment",
		body:       `{"items":{"9STARTS_WITH_DIGIT":"x"}}`,
		wantStatus: http.StatusUnprocessableEntity,
		// The spec does not constrain variable names (EnvironmentVariableItems
		// is a plain additionalProperties map), so the request satisfies the
		// document while the backend, and the mock, reject it with a 422.
		badRequest: false,
	},
	{
		name:       "logs 422 malformed cursor",
		specPath:   "/v1/hyperlift/applications/{id}/logs",
		method:     http.MethodGet,
		pathParams: seededParams(),
		path:       "/hyperlift/applications/" + seededID + "/logs",
		// "!!!" violates the cursor pattern ^[A-Za-z0-9_-]{1,64}$, so the spec
		// rejects the request too.
		query:      "?take=2&cursor=%21%21%21",
		wantStatus: http.StatusUnprocessableEntity,
		badRequest: true,
	},
}

// TestMockSatisfiesPublishedContract sends one request per route the mock
// serves, plus the error responses. It validates both the request and the mock's
// response against the vendored OpenAPI document.
func TestMockSatisfiesPublishedContract(t *testing.T) {
	ctx := context.Background()
	doc := loadSpec(t)

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			pathItem := doc.Paths.Value(sc.specPath)
			if pathItem == nil {
				t.Fatalf("spec has no path %q", sc.specPath)
			}

			op := pathItem.GetOperation(sc.method)
			if op == nil {
				t.Fatalf("spec has no %s on %q", sc.method, sc.specPath)
			}

			// Each scenario gets a new server. The mock keeps state, and a
			// mutation must not reach the next scenario.
			srv := httptest.NewServer(Server())
			defer srv.Close()

			status, header, respBody := call(t, srv.URL, sc)

			wantStatus := sc.wantStatus
			if wantStatus == 0 {
				wantStatus = http.StatusOK
			}

			if status != wantStatus {
				t.Fatalf("mock answered %d, want %d\nbody: %s", status, wantStatus, respBody)
			}

			// The request under validation carries the spec's /v1 prefix, so the
			// path parameters above line up. The mock itself answers on its own
			// path.
			req := httptest.NewRequestWithContext(ctx, sc.method, "/v1"+sc.path+sc.query, bodyReader(sc.body))
			if sc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}

			input := &openapi3filter.RequestValidationInput{
				Request:    req,
				PathParams: sc.pathParams,
				Route: &routers.Route{
					Spec:      doc,
					Path:      sc.specPath,
					PathItem:  pathItem,
					Method:    sc.method,
					Operation: op,
				},
				Options: validationOptions(),
			}

			err := openapi3filter.ValidateRequest(ctx, input)
			switch {
			case sc.badRequest && err == nil:
				t.Fatal("request should violate the contract, since the mock rejects it")
			case !sc.badRequest && err != nil:
				t.Fatalf("request does not satisfy the contract: %v", err)
			}

			out := &openapi3filter.ResponseValidationInput{
				RequestValidationInput: input,
				Status:                 status,
				Header:                 header,
				Options:                input.Options,
			}
			out.SetBodyBytes(respBody)

			if err := openapi3filter.ValidateResponse(ctx, out); err != nil {
				t.Fatalf("%d response does not satisfy the contract: %v\nbody: %s", status, err, respBody)
			}
		})
	}
}

// TestContractCoverage fails when the document gains a Hyperlift operation that
// no scenario drives, so a new contract operation cannot pass unnoticed.
func TestContractCoverage(t *testing.T) {
	doc := loadSpec(t)

	covered := make(map[string]bool, len(scenarios))
	for _, sc := range scenarios {
		covered[sc.method+" "+sc.specPath] = true
	}

	for path, item := range doc.Paths.Map() {
		if !strings.HasPrefix(path, specPrefix) {
			continue
		}

		for method := range item.Operations() {
			if !covered[method+" "+path] {
				t.Errorf("no scenario drives %s %s", method, path)
			}
		}
	}
}

func loadSpec(t *testing.T) *openapi3.T {
	t.Helper()

	ctx := context.Background()
	loader := &openapi3.Loader{Context: ctx}

	doc, err := loader.LoadFromFile(specFile)
	if err != nil {
		t.Fatalf("load %s: %v", specFile, err)
	}

	repairNullableRefs(doc.Components.Schemas)

	// The whole-document check waives two constructs the published artifact uses
	// outside the Hyperlift namespace: a DNS pattern with a Perl lookahead Go
	// cannot compile, and a `description` next to a `$ref`. Per-request and
	// per-response validation below runs with every check enabled.
	if err := doc.Validate(ctx,
		openapi3.DisableSchemaPatternValidation(),
		openapi3.DisableExamplesValidation(),
		openapi3.AllowExtraSiblingFields("description"),
	); err != nil {
		t.Fatalf("vendored spec is invalid: %v", err)
	}

	return doc
}

// validationOptions sets the strictness of every request and response check. An
// undeclared response status fails and every violation is reported. It waives
// gateway authentication, out of scope for the mock, and deliberately omits
// RejectWhenRequestBodyNotSpecified, which rejects any request with a body.
func validationOptions() *openapi3filter.Options {
	return &openapi3filter.Options{
		AuthenticationFunc:    openapi3filter.NoopAuthenticationFunc,
		IncludeResponseStatus: true,
		MultiError:            true,
	}
}

// repairNullableRefs works around TypeSpec bug #5156 in the published document. A
// nullable field that references another schema comes out as
//
//	branch:
//	  type: object                              <- the bug
//	  allOf:
//	    - $ref: '#/components/schemas/gitBranch' (a string)
//	  nullable: true
//
// The extra `type: object` contradicts the referenced string, so read literally,
// no value can satisfy the field. The vendored file must stay an exact copy of
// the published artifact, so the repair happens in memory: the wrapper takes the
// referenced schema's type, which is what the fixed emitter writes. Delete this
// after the document is fixed upstream.
// https://github.com/microsoft/typespec/issues/5156
func repairNullableRefs(schemas openapi3.Schemas) {
	seen := map[*openapi3.Schema]bool{}

	var walk func(ref *openapi3.SchemaRef)

	walk = func(ref *openapi3.SchemaRef) {
		if ref == nil || ref.Value == nil || seen[ref.Value] {
			return
		}

		s := ref.Value
		seen[s] = true

		if s.Type.Is("object") && s.Nullable && len(s.Properties) == 0 &&
			len(s.AllOf) == 1 && s.AllOf[0].Ref != "" {
			if inner := s.AllOf[0].Value; inner != nil && inner.Type != nil {
				s.Type = inner.Type
			}
		}

		for _, subs := range []openapi3.SchemaRefs{s.AllOf, s.AnyOf, s.OneOf} {
			for _, sub := range subs {
				walk(sub)
			}
		}

		for _, sub := range s.Properties {
			walk(sub)
		}

		walk(s.Items)
		walk(s.AdditionalProperties.Schema)
	}

	for _, ref := range schemas {
		walk(ref)
	}
}

// call sends the scenario's request to the running mock. It returns the status,
// the headers and the body to validate.
func call(t *testing.T, base string, sc scenario) (int, http.Header, []byte) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), sc.method, base+sc.path+sc.query, bodyReader(sc.body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if sc.body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", sc.method, sc.path, err)
	}

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return resp.StatusCode, resp.Header, body
}

func bodyReader(body string) io.Reader {
	if body == "" {
		return nil
	}

	return strings.NewReader(body)
}
