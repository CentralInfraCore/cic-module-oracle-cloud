// Fixture for OCI's polymorphic models: a *Details declared as an interface,
// with the concrete shape chosen by a discriminator. 1014 of the SDK's models
// are interfaces, 470 of them named *Details or *Base. Treated as a struct they
// look like a model with no fields, which yields a resource with an empty create
// surface and no error — so the extractor must record the kind and report it.
// Parsed with go/ast only; never compiled.
package database

// CreateBackupDestinationDetails is polymorphic: the concrete body is one of the
// implementations below, selected by the "type" discriminator.
type CreateBackupDestinationDetails interface {
	GetDisplayName() *string

	GetCompartmentId() *string
}

// CreateNfsBackupDestinationDetails is one concrete implementation.
type CreateNfsBackupDestinationDetails struct {
	DisplayName *string `mandatory:"true" json:"displayName"`

	CompartmentId *string `mandatory:"true" json:"compartmentId"`

	MountTypeDetails *string `mandatory:"false" json:"mountTypeDetails"`
}

// BackupDestination is the observed state — an ordinary struct.
type BackupDestination struct {
	Id *string `mandatory:"true" json:"id"`

	DisplayName *string `mandatory:"false" json:"displayName"`

	CompartmentId *string `mandatory:"true" json:"compartmentId"`
}
