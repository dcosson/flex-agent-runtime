// Package instance defines the InstanceProvisioner interface and associated
// types for cloud instance lifecycle management. This is a shared package
// imported by both the fleet manager (internal/sandbox/control/fleet) and the
// direct adapter (internal/sandbox/control/direct) independently, avoiding
// coupling between the two adapter packages.
//
// The InstanceProvisioner interface abstracts cloud provider instance
// operations (launch, terminate, stop, start, describe, list) behind a
// provider-neutral API. Concrete implementations exist per cloud provider
// (e.g., EC2InstanceProvisioner for AWS) and live in their own sub-packages.
//
// CloudInstanceState constants normalize cloud-specific instance states
// (e.g., EC2 "shutting-down", GCP "STAGING") to a minimal provider-neutral
// set used throughout the fleet and direct control layers.
package instance
