//go:build !wasip1

package main

// canonical_json_test.go pins cic-canonical-json/1 (docs/design/specs/canonical-json.md).
// The expectations live in testdata/canonical_json_vectors.json so that another
// implementation can be checked against the same file: Go is not the definition.

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"math/rand"
	"os"
	"strings"
	"testing"
)

type canonVectors struct {
	Format    string                                 `json:"format"`
	Canonical []struct{ Name, Input, Output string } `json:"canonical"`
	Equal     []struct{ Name, A, B string }          `json:"equal"`
	Distinct  []struct{ Name, A, B string }          `json:"distinct"`
	Reject    []struct {
		Name     string `json:"name"`
		Input    string `json:"input"`
		InputHex string `json:"input_hex"`
	} `json:"reject"`
}

func loadCanonVectors(t *testing.T) canonVectors {
	t.Helper()
	raw, err := os.ReadFile("testdata/canonical_json_vectors.json")
	if err != nil {
		t.Fatalf("vectors: %v", err)
	}
	var v canonVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("vectors are not valid JSON: %v", err)
	}
	if v.Format != "cic-canonical-json/1" {
		t.Fatalf("vector format = %q", v.Format)
	}
	if len(v.Canonical) == 0 || len(v.Equal) == 0 || len(v.Distinct) == 0 || len(v.Reject) == 0 {
		t.Fatalf("a vector group is empty: the test would check nothing")
	}
	return v
}

func TestCanonicalJSONProducesTheDocumentedForm(t *testing.T) {
	for _, c := range loadCanonVectors(t).Canonical {
		t.Run(c.Name, func(t *testing.T) {
			got, err := canonicalJSON([]byte(c.Input))
			if err != nil {
				t.Fatalf("refused %q: %v", c.Input, err)
			}
			if string(got) != c.Output {
				t.Errorf("canonical form of %q = %q, want %q", c.Input, got, c.Output)
			}
		})
	}
}

func TestEquivalentTextsShareOneCanonicalForm(t *testing.T) {
	for _, c := range loadCanonVectors(t).Equal {
		t.Run(c.Name, func(t *testing.T) {
			if !canonicalEqual(json.RawMessage(c.A), json.RawMessage(c.B)) {
				t.Errorf("%q and %q must be equal", c.A, c.B)
			}
		})
	}
}

func TestDifferentValuesNeverShareACanonicalForm(t *testing.T) {
	for _, c := range loadCanonVectors(t).Distinct {
		t.Run(c.Name, func(t *testing.T) {
			if canonicalEqual(json.RawMessage(c.A), json.RawMessage(c.B)) {
				t.Errorf("%q and %q are different values and must not be equal", c.A, c.B)
			}
		})
	}
}

func TestInputThatIsNotStrictJSONIsRefused(t *testing.T) {
	for _, c := range loadCanonVectors(t).Reject {
		t.Run(c.Name, func(t *testing.T) {
			in := []byte(c.Input)
			if c.InputHex != "" {
				b, err := hex.DecodeString(c.InputHex)
				if err != nil {
					t.Fatalf("bad hex in the vectors: %v", err)
				}
				in = b
			}
			if out, err := canonicalJSON(in); err == nil {
				t.Errorf("accepted %q, canonical %q", in, out)
			}
			// A refused input is never equal to anything, not even to itself.
			if canonicalEqual(json.RawMessage(in), json.RawMessage(in)) {
				t.Errorf("a refused input must not compare equal to itself")
			}
		})
	}
}

func TestCanonicalFormIsDeterministicAndIdempotent(t *testing.T) {
	v := loadCanonVectors(t)
	var inputs []string
	for _, c := range v.Canonical {
		inputs = append(inputs, c.Input)
	}
	for _, c := range v.Equal {
		inputs = append(inputs, c.A, c.B)
	}
	for _, in := range inputs {
		a, err := canonicalJSON([]byte(in))
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		b, _ := canonicalJSON([]byte(in))
		if !bytes.Equal(a, b) {
			t.Errorf("%q: two runs differ", in)
		}
		again, err := canonicalJSON(a)
		if err != nil || !bytes.Equal(again, a) {
			t.Errorf("%q: canonical form %q is not a fixed point (%q, %v)", in, a, again, err)
		}
	}
}

func TestCanonicalJSONBounds(t *testing.T) {
	nest := func(n int) string { return strings.Repeat("[", n) + strings.Repeat("]", n) }
	if _, err := canonicalJSON([]byte(nest(64))); err != nil {
		t.Errorf("depth 64 must be accepted: %v", err)
	}
	if _, err := canonicalJSON([]byte(nest(65))); err == nil {
		t.Errorf("depth 65 must be refused")
	}
	if _, err := canonicalJSON([]byte(nest(100000))); err == nil {
		t.Errorf("a very deep input must be refused, not recursed into")
	}
	if _, err := canonicalJSON([]byte("1" + strings.Repeat("0", 999))); err != nil {
		t.Errorf("a 1000-digit number must be accepted: %v", err)
	}
	if _, err := canonicalJSON([]byte("1" + strings.Repeat("0", 1000))); err == nil {
		t.Errorf("a 1001-digit number must be refused")
	}
	big := `"` + strings.Repeat("a", 1<<20) + `"`
	if _, err := canonicalJSON([]byte(big)); err == nil {
		t.Errorf("an input over 1 MiB must be refused")
	}
	ok := `"` + strings.Repeat("a", (1<<20)-2) + `"`
	if _, err := canonicalJSON([]byte(ok)); err != nil {
		t.Errorf("an input of exactly 1 MiB must be accepted: %v", err)
	}
	if _, err := canonicalJSON([]byte("1e10000")); err != nil {
		t.Errorf("exponent 10000 must be accepted: %v", err)
	}
}

// Mutated and random input must never panic, and whatever is accepted must be a
// fixed point of the canonical form.
func TestCanonicalJSONNeverPanicsOnMutatedInput(t *testing.T) {
	seeds := []string{`{"a":[1,2.5e3,"xé",true,null],"b":{"c":"😀"}}`, `[-0.0,1E2,"\"\\"]`, `{"k":{"k":{"k":[]}}}`}
	rng := rand.New(rand.NewSource(20261108))
	for i := 0; i < 4000; i++ {
		b := []byte(seeds[i%len(seeds)])
		for j := 0; j < 1+rng.Intn(4); j++ {
			switch rng.Intn(3) {
			case 0:
				b[rng.Intn(len(b))] = byte(rng.Intn(256))
			case 1:
				p := rng.Intn(len(b))
				b = append(b[:p], b[p+1:]...)
			default:
				p := rng.Intn(len(b) + 1)
				b = append(b[:p], append([]byte{byte(rng.Intn(256))}, b[p:]...)...)
			}
			if len(b) == 0 {
				b = []byte("0")
			}
		}
		out, err := canonicalJSON(b)
		if err != nil {
			continue
		}
		again, err := canonicalJSON(out)
		if err != nil || !bytes.Equal(again, out) {
			t.Fatalf("accepted %q but its canonical form %q is not a fixed point", b, out)
		}
	}
}
