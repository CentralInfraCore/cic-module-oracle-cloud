// Fixture models for the compute client. The create model is named
// LaunchInstanceDetails — there is no CreateInstanceDetails — so a name-template
// lookup finds nothing and silently emits a resource with no create surface.
// Parsed with go/ast only; never compiled.
package core

// LaunchInstanceDetails is the create projection of an instance.
type LaunchInstanceDetails struct {
	// CompartmentId is the OCID of the compartment to contain the instance.
	CompartmentId *string `mandatory:"true" json:"compartmentId"`

	AvailabilityDomain *string `mandatory:"true" json:"availabilityDomain"`

	Shape *string `mandatory:"false" json:"shape"`

	DisplayName *string `mandatory:"false" json:"displayName"`

	FreeformTags map[string]string `mandatory:"false" json:"freeformTags"`
}

// UpdateInstanceDetails is the mutable subset.
type UpdateInstanceDetails struct {
	DisplayName *string `mandatory:"false" json:"displayName"`

	Shape *string `mandatory:"false" json:"shape"`

	FreeformTags map[string]string `mandatory:"false" json:"freeformTags"`
}

// ChangeInstanceCompartmentDetails is the action body that governs compartmentId.
type ChangeInstanceCompartmentDetails struct {
	CompartmentId *string `mandatory:"true" json:"compartmentId"`
}

// Instance is the observed state.
type Instance struct {
	Id *string `mandatory:"true" json:"id"`

	CompartmentId *string `mandatory:"true" json:"compartmentId"`

	AvailabilityDomain *string `mandatory:"true" json:"availabilityDomain"`

	Shape *string `mandatory:"false" json:"shape"`

	DisplayName *string `mandatory:"false" json:"displayName"`

	FreeformTags map[string]string `mandatory:"false" json:"freeformTags"`

	LifecycleState InstanceLifecycleStateEnum `mandatory:"false" json:"lifecycleState"`
}

// LaunchInstanceRequest carries the create body, tagged so resolution can read
// the body model off the request rather than guessing it from the name.
type LaunchInstanceRequest struct {
	LaunchInstanceDetails `contributesTo:"body"`

	OpcRetryToken *string `mandatory:"false" contributesTo:"header" name:"opc-retry-token"`
}

// GetInstanceRequest addresses one instance by a single path parameter.
type GetInstanceRequest struct {
	InstanceId *string `mandatory:"true" contributesTo:"path" name:"instanceId"`
}

// GetInstanceResponse carries the read model in its body.
type GetInstanceResponse struct {
	Instance `presentIn:"body"`

	Etag *string `presentIn:"header" name:"etag"`
}
