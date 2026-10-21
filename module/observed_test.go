//go:build !wasip1

package main

// observed_test.go checks normalizeObserved against the common VCN fixture
// (testdata/vcn-v1, stage 3: expected/observed.oci.json) and against the rules
// that matter beyond it. The fixture's expected files are a projection to the
// compared OCI paths (cidrBlocks, dnsLabel, freeformTags, isIpv6Enabled for the
// config; id, lifecycleState, ipv6CidrBlocks for the state); the normalizer
// returns the whole effective_config, state and one coverage entry per contract
// config field, so the comparison is made on that projection.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const fxDir = "testdata/vcn-v1"
const vcnKind = "cic:network:vcn"

func readFile(t *testing.T, parts ...string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{fxDir}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func jsonOf(t *testing.T, raw []byte) interface{} {
	t.Helper()
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("not JSON: %v (%s)", err, raw)
	}
	return v
}

func TestVCNFixtureIsTheVendoredOne(t *testing.T) {
	listed := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(readFile(t, "MANIFEST.sha256"))), "\n") {
		f := strings.SplitN(line, "  ", 2)
		if len(f) != 2 {
			t.Fatalf("bad manifest line %q", line)
		}
		sum := sha256.Sum256(readFile(t, f[1]))
		if hex.EncodeToString(sum[:]) != f[0] {
			t.Errorf("%s differs from the vendored digest", f[1])
		}
		listed[f[1]] = true
	}
	if len(listed) < 50 {
		t.Fatalf("manifest lists only %d files", len(listed))
	}
	// no file in the case tree that the manifest does not know
	filepath.Walk(filepath.Join(fxDir, "cases"), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			rel, _ := filepath.Rel(fxDir, p)
			if !listed[filepath.ToSlash(rel)] {
				t.Errorf("%s is not in the manifest", rel)
			}
		}
		return nil
	})
}

func outcomeOf(t *testing.T, caseID string) getOutcome {
	t.Helper()
	if _, err := os.Stat(filepath.Join(fxDir, "cases", caseID, "oci", "response.json")); err == nil {
		return getOutcome{Status: 200, Body: readFile(t, "cases", caseID, "oci", "response.json")}
	}
	var e struct {
		Kind    string `json:"kind"`
		Status  int    `json:"status"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(readFile(t, "cases", caseID, "oci", "error.json"), &e); err != nil {
		t.Fatal(err)
	}
	if e.Kind == "transport" {
		return getOutcome{Transport: e.Message}
	}
	return getOutcome{Status: e.Status, Body: []byte(`{"code":"` + e.Code + `"}`)}
}

func fixtureCases(t *testing.T) []string {
	var m map[string]string
	if err := json.Unmarshal(readFile(t, "cases.json"), &m); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) != 9 {
		t.Fatalf("expected 9 cases, got %d", len(ids))
	}
	return ids
}

func TestNormalizeObservedMatchesTheFixture(t *testing.T) {
	for _, id := range fixtureCases(t) {
		t.Run(id, func(t *testing.T) {
			got, ok := normalizeObserved(vcnKind, outcomeOf(t, id))
			if !ok {
				t.Fatal("no contract for the VCN kind")
			}
			var want struct {
				EffectiveConfig map[string]json.RawMessage `json:"effective_config"`
				State           map[string]json.RawMessage `json:"state"`
				Coverage        map[string]string          `json:"coverage"`
			}
			if err := json.Unmarshal(readFile(t, "cases", id, "expected", "observed.oci.json"), &want); err != nil {
				t.Fatal(err)
			}
			// coverage: the compared paths exactly
			for p, w := range want.Coverage {
				if string(got.Coverage[p]) != w {
					t.Errorf("coverage %s = %q, want %q", p, got.Coverage[p], w)
				}
			}
			// effective_config: on the compared fields, the same values and the same presence
			for p := range want.Coverage {
				name := strings.TrimPrefix(p, "/")
				w, wantHas := want.EffectiveConfig[name]
				g, gotHas := got.EffectiveConfig[name]
				if wantHas != gotHas || (wantHas && !reflect.DeepEqual(jsonOf(t, w), jsonOf(t, g))) {
					t.Errorf("effective_config.%s = %s (present %v), want %s (present %v)", name, g, gotHas, w, wantHas)
				}
			}
			// state: the fixture's state fields, and nothing it lists missing
			for _, name := range []string{"id", "lifecycleState", "ipv6CidrBlocks"} {
				w, wantHas := want.State[name]
				g, gotHas := got.State[name]
				if wantHas != gotHas || (wantHas && !reflect.DeepEqual(jsonOf(t, w), jsonOf(t, g))) {
					t.Errorf("state.%s = %s (present %v), want %s (present %v)", name, g, gotHas, w, wantHas)
				}
			}
		})
	}
}

func TestCoverageNeverClaimsMoreThanWasRead(t *testing.T) {
	for _, id := range fixtureCases(t) {
		got, _ := normalizeObserved(vcnKind, outcomeOf(t, id))
		c := resourceContracts()[vcnKind]
		if len(got.Coverage) != len(c.fields) {
			t.Errorf("%s: %d coverage entries for %d contract fields", id, len(got.Coverage), len(c.fields))
		}
		for p, s := range got.Coverage {
			name := strings.TrimPrefix(p, "/")
			_, inEff := got.EffectiveConfig[name]
			switch s {
			case covObserved:
				if !inEff {
					t.Errorf("%s: %s is observed but has no value in effective_config", id, p)
				}
			case covAbsent, covUnobserved, covUnknown:
				if inEff {
					t.Errorf("%s: %s is %s but effective_config carries a value", id, p, s)
				}
			default:
				t.Errorf("%s: %s has coverage %q", id, p, s)
			}
		}
	}
}

func TestAPartialReadNeverFabricatesAValue(t *testing.T) {
	got, _ := normalizeObserved(vcnKind, outcomeOf(t, "partial-read"))
	if _, has := got.EffectiveConfig["isIpv6Enabled"]; has {
		t.Fatal("isIpv6Enabled was derived from a source that is not in the answer")
	}
	if got.Coverage["/isIpv6Enabled"] != covUnobserved {
		t.Fatalf("coverage = %q, want unobserved (not false)", got.Coverage["/isIpv6Enabled"])
	}
}

func TestAnErrorAnswerIsNeverAuthoritativeAbsence(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 409, 429, 500, 503} {
		got, _ := normalizeObserved(vcnKind, getOutcome{Status: status, Body: []byte(`{"code":"NotAuthorizedOrNotFound"}`)})
		for p, s := range got.Coverage {
			if s != covUnknown {
				t.Errorf("HTTP %d: %s is %q, want unknown", status, p, s)
			}
		}
		if len(got.EffectiveConfig) != 0 || len(got.State) != 0 {
			t.Errorf("HTTP %d: the error body leaked into the observed state", status)
		}
	}
	got, _ := normalizeObserved(vcnKind, getOutcome{Transport: "timeout"})
	for p, s := range got.Coverage {
		if s != covUnobserved {
			t.Errorf("transport failure: %s is %q, want unobserved", p, s)
		}
	}
}

func TestAnAnswerThatIsNotAnObjectIsUnknown(t *testing.T) {
	for _, body := range []string{``, `not json`, `[]`, `null`, `"x"`} {
		got, _ := normalizeObserved(vcnKind, getOutcome{Status: 200, Body: []byte(body)})
		for p, s := range got.Coverage {
			if s != covUnknown {
				t.Errorf("body %q: %s is %q, want unknown", body, p, s)
			}
		}
	}
}

func TestOnlyDeclaredFieldsAreEverAbsent(t *testing.T) {
	// an omitted field is unknown unless the kind declares that omission means "not set"
	for _, kind := range []string{"cic:network:subnet", "cic:compute:instance"} {
		c, ok := resourceContracts()[kind]
		if !ok {
			continue
		}
		got, _ := normalizeObserved(kind, getOutcome{Status: 200, Body: []byte(`{"id":"x"}`)})
		for p, s := range got.Coverage {
			if s == covAbsent {
				t.Errorf("%s %s: absent is claimed for a field no one declared as unset-when-omitted", kind, p)
			}
		}
		_ = c
	}
	got, _ := normalizeObserved(vcnKind, getOutcome{Status: 200, Body: []byte(`{"id":"x"}`)})
	for p, s := range got.Coverage {
		want := covUnknown
		switch p {
		case "/dnsLabel":
			want = covAbsent
		case "/isIpv6Enabled", "/isOracleGuaAllocationEnabled", "/byoipv6CidrDetails":
			want = covUnobserved
		}
		if s != want {
			t.Errorf("VCN %s with an almost empty answer = %q, want %q", p, s, want)
		}
	}
}

func TestInputOnlyFieldsThatAreNotDerivedAreUnobserved(t *testing.T) {
	got, _ := normalizeObserved(vcnKind, getOutcome{Status: 200, Body: []byte(`{"isOracleGuaAllocationEnabled":true,"byoipv6CidrDetails":[]}`)})
	for _, p := range []string{"/isOracleGuaAllocationEnabled", "/byoipv6CidrDetails"} {
		if got.Coverage[p] != covUnobserved {
			t.Errorf("%s = %q: an input-only field is not observed even if an answer happens to carry it", p, got.Coverage[p])
		}
	}
}

func TestNormalizeObservedRefusesAKindWithoutAContract(t *testing.T) {
	if _, ok := normalizeObserved("cic:nothing:here", getOutcome{Status: 200, Body: []byte(`{}`)}); ok {
		t.Fatal("a kind without a contract was normalized")
	}
}

func TestObservedGoDoesNotReachTheHost(t *testing.T) {
	// the function is pure: the source of observed.go names none of the host-facing calls
	src := string(func() []byte { b, _ := os.ReadFile("observed.go"); return b }())
	for _, bad := range []string{"actuateSigned", "nowFunc", "net/http", "os.", "callHost"} {
		if strings.Contains(strings.SplitN(src, "package main", 2)[1], bad) {
			t.Errorf("observed.go mentions %q", bad)
		}
	}
}
