// Fixture mirroring the shape of the OCI Go SDK's core VCN models. Lives under
// testdata/ so the Go toolchain never compiles it; it is only ever read by
// go/ast in the extractor tests. Hand-written to match the real SDK's tag
// conventions (mandatory / json / contributesTo / presentIn / name).
package core

// CreateVcnDetails is the create projection of a VCN's desired state.
type CreateVcnDetails struct {
	// CompartmentId is the OCID of the compartment to contain the VCN.
	CompartmentId *string `mandatory:"true" json:"compartmentId"`

	CidrBlocks []string `mandatory:"false" json:"cidrBlocks"`

	DisplayName *string `mandatory:"false" json:"displayName"`

	DnsLabel *string `mandatory:"false" json:"dnsLabel"`

	FreeformTags map[string]string `mandatory:"false" json:"freeformTags"`
}

// UpdateVcnDetails is the update projection — the subset a caller may change
// after creation. Absence of a field here does NOT by itself mean immutable: a
// field may instead be changed through an action model (see below).
type UpdateVcnDetails struct {
	CidrBlocks []string `mandatory:"false" json:"cidrBlocks"`

	DisplayName *string `mandatory:"false" json:"displayName"`

	FreeformTags map[string]string `mandatory:"false" json:"freeformTags"`
}

// ChangeVcnCompartmentDetails is an action model: compartmentId is not in
// UpdateVcnDetails, but it is mutable through this action — so the classifier
// must label compartmentId "action", not "create-only".
type ChangeVcnCompartmentDetails struct {
	CompartmentId *string `mandatory:"true" json:"compartmentId"`
}

// Vcn is the observed state read back from the service.
type Vcn struct {
	Id *string `mandatory:"true" json:"id"`

	CompartmentId *string `mandatory:"true" json:"compartmentId"`

	CidrBlocks []string `mandatory:"false" json:"cidrBlocks"`

	DisplayName *string `mandatory:"false" json:"displayName"`

	DnsLabel *string `mandatory:"false" json:"dnsLabel"`

	LifecycleState string `mandatory:"false" json:"lifecycleState"`

	TimeCreated *string `mandatory:"false" json:"timeCreated"`
}

// CreateVcnRequest wraps the body and the OCI request headers.
type CreateVcnRequest struct {
	CreateVcnDetails `contributesTo:"body"`

	OpcRetryToken *string `mandatory:"false" contributesTo:"header" name:"opc-retry-token"`

	OpcRequestId *string `mandatory:"false" contributesTo:"header" name:"opc-request-id"`
}

// CreateVcnResponse carries the created resource and the response headers.
type CreateVcnResponse struct {
	Vcn `presentIn:"body"`

	Etag *string `presentIn:"header" name:"etag"`

	OpcRequestId *string `presentIn:"header" name:"opc-request-id"`
}
