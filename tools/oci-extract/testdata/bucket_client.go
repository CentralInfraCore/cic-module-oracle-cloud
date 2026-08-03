// Fixture mirroring the OCI SDK's object storage client. Two things here break
// assumptions that hold for core/network: the resource is addressed by TWO path
// parameters (neither of which is an id), and it is updated with POST because
// the service offers no PUT on the resource path. Parsed with go/parser only.
package objectstorage

import (
	"context"
	"net/http"
)

// ObjectStorageClient is a stand-in for the SDK client receiver type.
type ObjectStorageClient struct{}

// CreateBucket creates a bucket in the given namespace.
func (client ObjectStorageClient) CreateBucket(ctx context.Context, request CreateBucketRequest) (response CreateBucketResponse, err error) {
	ociResponse, err := common_Retry(ctx, request, client.createBucket)
	_ = ociResponse
	return
}

func (client ObjectStorageClient) createBucket(ctx context.Context, request OCIRequest) (OCIResponse, error) {
	httpRequest, err := request.HTTPRequest(http.MethodPost, "/n/{namespaceName}/b", nil, nil)
	_ = httpRequest
	return nil, err
}

// GetBucket gets the current representation of the given bucket.
func (client ObjectStorageClient) GetBucket(ctx context.Context, request GetBucketRequest) (response GetBucketResponse, err error) {
	ociResponse, err := common_Retry(ctx, request, client.getBucket)
	_ = ociResponse
	return
}

func (client ObjectStorageClient) getBucket(ctx context.Context, request OCIRequest) (OCIResponse, error) {
	httpRequest, err := request.HTTPRequest(http.MethodGet, "/n/{namespaceName}/b/{bucketName}", nil, nil)
	_ = httpRequest
	return nil, err
}

// UpdateBucket performs a partial or full update of a bucket — with POST, not
// PUT; there is no PUT on this path at all.
func (client ObjectStorageClient) UpdateBucket(ctx context.Context, request UpdateBucketRequest) (response UpdateBucketResponse, err error) {
	ociResponse, err := common_Retry(ctx, request, client.updateBucket)
	_ = ociResponse
	return
}

func (client ObjectStorageClient) updateBucket(ctx context.Context, request OCIRequest) (OCIResponse, error) {
	httpRequest, err := request.HTTPRequest(http.MethodPost, "/n/{namespaceName}/b/{bucketName}", nil, nil)
	_ = httpRequest
	return nil, err
}

// DeleteBucket deletes a bucket if it is already empty.
func (client ObjectStorageClient) DeleteBucket(ctx context.Context, request DeleteBucketRequest) (response DeleteBucketResponse, err error) {
	ociResponse, err := common_Retry(ctx, request, client.deleteBucket)
	_ = ociResponse
	return
}

func (client ObjectStorageClient) deleteBucket(ctx context.Context, request OCIRequest) (OCIResponse, error) {
	httpRequest, err := request.HTTPRequest(http.MethodDelete, "/n/{namespaceName}/b/{bucketName}", nil, nil)
	_ = httpRequest
	return nil, err
}
