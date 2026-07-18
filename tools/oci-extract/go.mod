// oci-extract — build-time extractor for the OCI schema pipeline (roadmap P2.2).
// It reads the pinned OCI Go SDK source (oci-sdk.lock.yaml) with go/ast and emits
// a machine-readable model registry. Standard library only: no runtime, no
// external deps, and nothing here ships in the WASM module.
module github.com/CentralInfraCore/cic-module-oracle-cloud/tools/oci-extract

go 1.25.0
