Vendored, byte for byte, from `fixtures/vcn-v1` of CentralInfraCore/cic-factory, commit
`e5978f50d2512c5ac7536b9d81ffb1f054b75525` (PR cic-factory#37). `MANIFEST.sha256` is the one from there and is checked by
`TestVCNFixtureIsTheVendoredOne`. `generate.py` is not copied (the Go repo has no use for it). Do not edit the other files: if one
looks wrong, say so upstream. This module owns stage 3 only (`expected/observed.oci.json`).
