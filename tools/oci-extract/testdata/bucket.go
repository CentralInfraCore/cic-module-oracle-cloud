// Fixture models for the object storage client. The resource is keyed by a name
// inside a namespace rather than by an OCID, so addressing it needs both path
// parameters — the read model alone does not tell a consumer how to reach it.
// Parsed with go/ast only; never compiled.
package objectstorage

// CreateBucketDetails is the create projection of a bucket.
type CreateBucketDetails struct {
	Name *string `mandatory:"true" json:"name"`

	CompartmentId *string `mandatory:"true" json:"compartmentId"`

	StorageTier *string `mandatory:"false" json:"storageTier"`

	FreeformTags map[string]string `mandatory:"false" json:"freeformTags"`
}

// UpdateBucketDetails is the mutable subset.
type UpdateBucketDetails struct {
	Namespace *string `mandatory:"false" json:"namespace"`

	FreeformTags map[string]string `mandatory:"false" json:"freeformTags"`
}

// Bucket is the observed state.
type Bucket struct {
	Namespace *string `mandatory:"true" json:"namespace"`

	Name *string `mandatory:"true" json:"name"`

	CompartmentId *string `mandatory:"true" json:"compartmentId"`

	StorageTier *string `mandatory:"false" json:"storageTier"`

	FreeformTags map[string]string `mandatory:"false" json:"freeformTags"`

	TimeCreated *string `mandatory:"true" json:"timeCreated"`
}
