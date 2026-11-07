//go:build !wasip1

package main

// observe_derivation_test.go guards one rule on the PRODUCTION Observe path: a
// field the provider did not return must never become an observed value.
//
// Concretely: isIpv6Enabled is input-only — OCI accepts it at create and reads
// it back as the ipv6CidrBlocks list (correspondence/vcn.json). Observe derives
// it with applyDerivations, which only fires when the source state field is
// present. If that source-presence guard were lost, a partial read (no
// ipv6CidrBlocks in the answer) would fabricate isIpv6Enabled=false — "not
// observed" silently becoming "observed false", which a reconciler would act on.
//
// Why these tests drive Observe itself and not normalizeObserved: missing, null,
// empty-list and explicit-false are FOUR distinct states, and normalizeObserved
// (observed.go) has no production caller — it post-filters effective_config, so
// a test routed through it passes even when the production derivation is broken.
// The table therefore carries positive controls: a "fix" that dropped
// isIpv6Enabled whenever it is false would satisfy the missing-key row while
// destroying the genuine "IPv6 disabled" observation in the empty-list row.
//
// This file adds NO production behaviour. The production Observe is unchanged;
// the RED->GREEN evidence for it is between an injected fault (deleting the
// applyDerivations source-presence guard) and the original code.

import (
	"encoding/base64"
	"encoding/json"
	"sort"
	"testing"
)

// observeVCNBody drives the production Observe against a mocked signed GET that
// answers with body, and returns the decoded observation.
func observeVCNBody(t *testing.T, body string) observation {
	t.Helper()
	testCallHostSign = func(req []byte) ([]byte, error) { return []byte(`{"signature":"S"}`), nil }
	testCallHostActuate = func(req []byte) ([]byte, error) {
		out, _ := json.Marshal(map[string]interface{}{
			"status":      200,
			"headers":     map[string]string{"etag": "etag-ipv6", "opc-request-id": "req-ipv6"},
			"body_base64": base64.StdEncoding.EncodeToString([]byte(body)),
		})
		return out, nil
	}
	defer func() { testCallHostSign = nil; testCallHostActuate = nil }()

	req, _ := json.Marshal(observeRequest{
		Kind:    "cic:network:vcn",
		Binding: execBinding{Host: "iaas.eu-frankfurt-1.oraclecloud.com", KeyID: "t/u/f", ResourceID: "ocid1.vcn..x"},
	})
	out, err := Observe(nil, req)
	if err != nil {
		t.Fatalf("Observe returned a Go error: %v", err)
	}
	res := decodeResult(t, out)
	if res.Status != "ok" {
		t.Fatalf("observe status = %q, want ok (raw: %s)", res.Status, out)
	}
	var obs observation
	if err := json.Unmarshal(res.Result, &obs); err != nil {
		t.Fatalf("observation decode: %v (raw: %s)", err, res.Result)
	}
	return obs
}

// jsonKeys returns the sorted top-level keys of a JSON object.
func jsonKeys(t *testing.T, raw string) []string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("fixture body is not a JSON object: %v", err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestObserveIPv6DerivationNeedsItsSource is the four-state table over the
// derivation source. Missing, empty list, non-empty list and null are distinct
// provider answers; only a present source may produce a derived value.
func TestObserveIPv6DerivationNeedsItsSource(t *testing.T) {
	// Every body carries the same settable fields; only the ipv6CidrBlocks entry
	// varies, so the single variable under test is the source field's presence.
	const common = `"id":"ocid1.vcn..x","compartmentId":"ocid1.compartment..c",` +
		`"displayName":"prod","dnsLabel":"prod","lifecycleState":"AVAILABLE",` +
		`"cidrBlocks":["10.0.0.0/16"],"timeCreated":"2026-07-19T00:00:00Z"`

	cases := []struct {
		name string
		// ipv6 is the raw `"ipv6CidrBlocks":<value>,` entry, or "" to omit the key.
		ipv6 string
		// wantPresent: is isIpv6Enabled expected in effective_config at all?
		wantPresent bool
		// wantValue is the expected raw JSON when wantPresent.
		wantValue string
		why       string
	}{
		{
			name:        "source key missing: no derived value at all",
			ipv6:        "",
			wantPresent: false,
			why:         "the read did not observe the source, so no observed value may exist — not false",
		},
		{
			name:        "source present and empty: observed false",
			ipv6:        `"ipv6CidrBlocks":[],`,
			wantPresent: true,
			wantValue:   "false",
			why:         "positive control: an empty list IS an observation — IPv6 is disabled",
		},
		{
			name:        "source present and non-empty: observed true",
			ipv6:        `"ipv6CidrBlocks":["2603:c020::/48"],`,
			wantPresent: true,
			wantValue:   "true",
			why:         "positive control: a non-empty list means IPv6 is enabled",
		},
		{
			name:        "source present and null: current behaviour recorded",
			ipv6:        `"ipv6CidrBlocks":null,`,
			wantPresent: true,
			wantValue:   "false",
			why: "MEASURED, not prescribed: the key is present, so the guard lets the " +
				"derivation run, and jsonNonEmpty treats null as empty -> false. This " +
				"DIFFERS from the missing-key row: a JSON null is treated as an " +
				"observation of emptiness, not as an absence of observation. Recorded " +
				"here so a future change to that reading has to break this line first.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := "{" + tc.ipv6 + common + "}"
			obs := observeVCNBody(t, body)

			got, present := obs.EffectiveConfig["isIpv6Enabled"]
			switch {
			case !tc.wantPresent && present:
				t.Errorf("effective_config.isIpv6Enabled = %s, want the key to be ABSENT (%s)", got, tc.why)
			case tc.wantPresent && !present:
				t.Errorf("effective_config.isIpv6Enabled is absent, want %s (%s)", tc.wantValue, tc.why)
			case tc.wantPresent && string(got) != tc.wantValue:
				t.Errorf("effective_config.isIpv6Enabled = %s, want %s (%s)", got, tc.wantValue, tc.why)
			}

			// state is the provider answer, verbatim: the derivation must not add
			// to it, drop from it, or rewrite it.
			wantKeys := jsonKeys(t, body)
			gotKeys := make([]string, 0, len(obs.State))
			for k := range obs.State {
				gotKeys = append(gotKeys, k)
			}
			sort.Strings(gotKeys)
			if len(gotKeys) != len(wantKeys) {
				t.Fatalf("state keys = %v, want exactly the response keys %v", gotKeys, wantKeys)
			}
			for i := range wantKeys {
				if gotKeys[i] != wantKeys[i] {
					t.Fatalf("state keys = %v, want exactly the response keys %v", gotKeys, wantKeys)
				}
			}
			var wantState map[string]json.RawMessage
			if err := json.Unmarshal([]byte(body), &wantState); err != nil {
				t.Fatalf("fixture body: %v", err)
			}
			for k, want := range wantState {
				if !canonicalEqual(want, obs.State[k]) {
					t.Errorf("state[%q] = %s, want the response value %s", k, obs.State[k], want)
				}
			}

			// isIpv6Enabled is input-only and never returned by the read, so it
			// must not appear in raw state either, whatever happened above.
			if _, leaked := obs.State["isIpv6Enabled"]; leaked {
				t.Errorf("state carries isIpv6Enabled, which the read never returns")
			}
		})
	}
}

// TestObserveDerivationLeavesOtherInputOnlyFieldsUnobserved pins the other half
// of the rule: an input-only field with NO derivation has no way to be observed,
// so Observe must never invent one. Without this, a derivation accidentally
// applied to every input-only field would pass the table above.
func TestObserveDerivationLeavesOtherInputOnlyFieldsUnobserved(t *testing.T) {
	// A full read: every ipv6 source present, nothing partial.
	obs := observeVCNBody(t, `{"id":"ocid1.vcn..x","compartmentId":"ocid1.compartment..c",`+
		`"displayName":"prod","cidrBlocks":["10.0.0.0/16"],"ipv6CidrBlocks":["2603:c020::/48"]}`)

	// byoipv6CidrDetails and isOracleGuaAllocationEnabled are input-only in
	// schemas/core/vcn.json and have no entry in correspondence/vcn.json.
	for _, f := range []string{"byoipv6CidrDetails", "isOracleGuaAllocationEnabled"} {
		if v, ok := obs.EffectiveConfig[f]; ok {
			t.Errorf("effective_config.%s = %s, want absent: it is input-only with no derivation, so it cannot be observed", f, v)
		}
	}
	// ...while the one field that DOES have a derivation is still there.
	if string(obs.EffectiveConfig["isIpv6Enabled"]) != "true" {
		t.Errorf("effective_config.isIpv6Enabled = %s, want true", obs.EffectiveConfig["isIpv6Enabled"])
	}
}
