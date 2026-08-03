// Fixture mirroring the one OCI operation that has no request object at all:
// IdentityClient.ListRegions takes only a context, so there is no *Request
// parameter to carry the wire call — its private half uses the package-level
// common.MakeDefaultHTTPRequest instead of request.HTTPRequest. Requiring a
// *Request parameter, or matching only the HTTPRequest method, drops it
// silently. Parsed with go/parser only; never compiled.
package identity

import (
	"context"
	"net/http"
)

// IdentityClient is a stand-in for the SDK client receiver type.
type IdentityClient struct{}

// ListRegions lists all the regions offered by Oracle Cloud Infrastructure.
func (client IdentityClient) ListRegions(ctx context.Context) (response ListRegionsResponse, err error) {
	ociResponse, err := client.listRegions(ctx)
	_ = ociResponse
	return
}

// listRegions performs the request (retry policy is not enabled without a
// request object).
func (client IdentityClient) listRegions(ctx context.Context) (OCIResponse, error) {
	httpRequest := common.MakeDefaultHTTPRequest(http.MethodGet, "/regions")
	_ = httpRequest
	return nil, nil
}

// CreateUser creates a new user in the tenancy — the ordinary shape, present so
// the fixture also covers a normal operation beside the request-less one.
func (client IdentityClient) CreateUser(ctx context.Context, request CreateUserRequest) (response CreateUserResponse, err error) {
	ociResponse, err := common_Retry(ctx, request, client.createUser)
	_ = ociResponse
	return
}

func (client IdentityClient) createUser(ctx context.Context, request OCIRequest) (OCIResponse, error) {
	httpRequest, err := request.HTTPRequest(http.MethodPost, "/users", nil, nil)
	_ = httpRequest
	return nil, err
}

// SetRegion is client plumbing, not an operation: no *Response result.
func (client *IdentityClient) SetRegion(region string) {
	_ = region
}
