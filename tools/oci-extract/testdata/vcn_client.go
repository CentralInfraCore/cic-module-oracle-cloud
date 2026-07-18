// This is testdata mimicking the OCI Go SDK's generated *_client.go shape. It is
// parsed with go/parser (never compiled or type-checked), so the imports and
// helper types are only enough to be syntactically valid Go. The Go tool ignores
// testdata/.
package core

import (
	"context"
	"net/http"
)

// VirtualNetworkClient is a stand-in for the SDK client receiver type.
type VirtualNetworkClient struct{}

// CreateVcn creates a new virtual cloud network.
func (client VirtualNetworkClient) CreateVcn(ctx context.Context, request CreateVcnRequest) (response CreateVcnResponse, err error) {
	ociResponse, err := common_Retry(ctx, request, client.createVcn)
	_ = ociResponse
	return
}

func (client VirtualNetworkClient) createVcn(ctx context.Context, request OCIRequest) (OCIResponse, error) {
	httpRequest, err := request.HTTPRequest(http.MethodPost, "/vcns", nil, nil)
	_ = httpRequest
	return nil, err
}

// GetVcn gets the specified VCN's information.
func (client VirtualNetworkClient) GetVcn(ctx context.Context, request GetVcnRequest) (response GetVcnResponse, err error) {
	ociResponse, err := common_Retry(ctx, request, client.getVcn)
	_ = ociResponse
	return
}

func (client VirtualNetworkClient) getVcn(ctx context.Context, request OCIRequest) (OCIResponse, error) {
	httpRequest, err := request.HTTPRequest(http.MethodGet, "/vcns/{vcnId}", nil, nil)
	_ = httpRequest
	return nil, err
}

// DeleteVcn deletes the specified VCN.
func (client VirtualNetworkClient) DeleteVcn(ctx context.Context, request DeleteVcnRequest) (response DeleteVcnResponse, err error) {
	ociResponse, err := common_Retry(ctx, request, client.deleteVcn)
	_ = ociResponse
	return
}

func (client VirtualNetworkClient) deleteVcn(ctx context.Context, request OCIRequest) (OCIResponse, error) {
	httpRequest, err := request.HTTPRequest(http.MethodDelete, "/vcns/{vcnId}", nil, nil)
	_ = httpRequest
	return nil, err
}

// ListVcns is a helper-shaped method WITHOUT a *Request/*Response signature and
// no HTTPRequest of its own; the extractor must skip it.
func (client VirtualNetworkClient) helperNoOp(ctx context.Context) error {
	return nil
}

// --- minimal stand-ins so the file parses ---

type OCIRequest interface {
	HTTPRequest(method, path string, a, b interface{}) (interface{}, error)
}
type OCIResponse interface{}

type CreateVcnRequest struct{}
type CreateVcnResponse struct{}
type GetVcnRequest struct{}
type GetVcnResponse struct{}
type DeleteVcnRequest struct{}
type DeleteVcnResponse struct{}

func common_Retry(ctx context.Context, req interface{}, fn interface{}) (OCIResponse, error) {
	return nil, nil
}
