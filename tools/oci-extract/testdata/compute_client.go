// Fixture mirroring the OCI SDK's core compute client. Its point is that the
// lifecycle verbs are NOT Create/Delete: an instance is created by LaunchInstance
// and destroyed by TerminateInstance. Resolution must find them from the HTTP
// surface (POST on the collection path, DELETE on the resource path), not from
// the Go identifiers. Parsed with go/parser only; never compiled.
package core

import (
	"context"
	"net/http"
)

// ComputeClient is a stand-in for the SDK client receiver type.
type ComputeClient struct{}

// LaunchInstance creates a new instance in the specified compartment.
func (client ComputeClient) LaunchInstance(ctx context.Context, request LaunchInstanceRequest) (response LaunchInstanceResponse, err error) {
	ociResponse, err := common_Retry(ctx, request, client.launchInstance)
	_ = ociResponse
	return
}

func (client ComputeClient) launchInstance(ctx context.Context, request OCIRequest) (OCIResponse, error) {
	httpRequest, err := request.HTTPRequest(http.MethodPost, "/instances", nil, nil)
	_ = httpRequest
	return nil, err
}

// GetInstance gets information about the specified instance.
func (client ComputeClient) GetInstance(ctx context.Context, request GetInstanceRequest) (response GetInstanceResponse, err error) {
	ociResponse, err := common_Retry(ctx, request, client.getInstance)
	_ = ociResponse
	return
}

func (client ComputeClient) getInstance(ctx context.Context, request OCIRequest) (OCIResponse, error) {
	httpRequest, err := request.HTTPRequest(http.MethodGet, "/instances/{instanceId}", nil, nil)
	_ = httpRequest
	return nil, err
}

// UpdateInstance updates certain fields on the specified instance.
func (client ComputeClient) UpdateInstance(ctx context.Context, request UpdateInstanceRequest) (response UpdateInstanceResponse, err error) {
	ociResponse, err := common_Retry(ctx, request, client.updateInstance)
	_ = ociResponse
	return
}

func (client ComputeClient) updateInstance(ctx context.Context, request OCIRequest) (OCIResponse, error) {
	httpRequest, err := request.HTTPRequest(http.MethodPut, "/instances/{instanceId}", nil, nil)
	_ = httpRequest
	return nil, err
}

// TerminateInstance terminates the specified instance.
func (client ComputeClient) TerminateInstance(ctx context.Context, request TerminateInstanceRequest) (response TerminateInstanceResponse, err error) {
	ociResponse, err := common_Retry(ctx, request, client.terminateInstance)
	_ = ociResponse
	return
}

func (client ComputeClient) terminateInstance(ctx context.Context, request OCIRequest) (OCIResponse, error) {
	httpRequest, err := request.HTTPRequest(http.MethodDelete, "/instances/{instanceId}", nil, nil)
	_ = httpRequest
	return nil, err
}

// ChangeInstanceCompartment moves an instance into a different compartment.
func (client ComputeClient) ChangeInstanceCompartment(ctx context.Context, request ChangeInstanceCompartmentRequest) (response ChangeInstanceCompartmentResponse, err error) {
	ociResponse, err := common_Retry(ctx, request, client.changeInstanceCompartment)
	_ = ociResponse
	return
}

func (client ComputeClient) changeInstanceCompartment(ctx context.Context, request OCIRequest) (OCIResponse, error) {
	httpRequest, err := request.HTTPRequest(http.MethodPost, "/instances/{instanceId}/actions/changeCompartment", nil, nil)
	_ = httpRequest
	return nil, err
}

// InstanceAction performs a power action on the instance. It POSTs to the
// resource path itself, so it must not be mistaken for the update operation —
// the PUT wins.
func (client ComputeClient) InstanceAction(ctx context.Context, request InstanceActionRequest) (response InstanceActionResponse, err error) {
	ociResponse, err := common_Retry(ctx, request, client.instanceAction)
	_ = ociResponse
	return
}

func (client ComputeClient) instanceAction(ctx context.Context, request OCIRequest) (OCIResponse, error) {
	httpRequest, err := request.HTTPRequest(http.MethodPost, "/instances/{instanceId}", nil, nil)
	_ = httpRequest
	return nil, err
}
