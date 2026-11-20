package redact

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodedCredentialFieldsAndShape(t *testing.T) {
	const private = "private-fixture"
	deep := strings.Repeat("[", 65) + `"` + private + `"` + strings.Repeat("]", 65)
	r := New(Options{Values: []string{"731942", "true"}})
	for _, input := range []string{
		`{"Authorization":["` + private + `",{"inner":"` + private + `"}],"note":"public"}`,
		`{"\u0041uthorization":123,"note":"public"}`,
		`{"Cookie":` + deep + `,"note":"public"}`,
		`{"msg":"sent Authorization: ` + private + `","note":"public"}`,
		`{"url":"https:\/\/u:` + private + `@host.test","note":"public"}`,
		`{"url":"https://u\u0026name:` + private + `@host.test","note":"public"}`,
		`{"list":[731942,true,9007199254740993],"note":"public"}`,
		`{"Authorization":"` + private + `","Authorization":"duplicate","note":"public"}`,
	} {
		got := r.Redact(input)
		if !json.Valid([]byte(got)) || !strings.Contains(got, "public") ||
			strings.Contains(
				got,
				private,
			) || strings.Contains(got, "731942") || strings.Contains(got, "true") {
			t.Fatalf("credential or record shape was not protected: %q", got)
		}
		assertAllStreamCuts(t, r, input+"\n", got+"\n")
	}
}

func TestDecodedOrdinaryKeysKeepTheirValues(t *testing.T) {
	r := New(Options{})
	input := `{"X-Authorization":"public","\\u0041uthorization":"public"}`
	output := r.Redact(input)
	if !json.Valid([]byte(output)) || strings.Count(output, "public") != 2 {
		t.Fatalf("decoded noncredential keys lost their values: %q", output)
	}
}

func TestJSONMaskAndNumberPrecision(t *testing.T) {
	mask := "quote\" and slash\\ and newline\n"
	r := New(Options{Mask: mask})
	input := `{"Authorization":true,"number":9007199254740993}`
	got := r.Redact(input)
	decoded := make(map[string]json.RawMessage)
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatal(err)
	}
	var field string
	if err := json.Unmarshal(decoded["Authorization"], &field); err != nil || field != mask {
		t.Fatalf("custom mask was not valid JSON: %s", got)
	}
	if string(decoded["number"]) != "9007199254740993" {
		t.Fatal("an unmasked integer lost precision")
	}
	assertAllStreamCuts(t, r, input, got)
}

func TestRegisteredJSONCompositeAndWireForms(t *testing.T) {
	const marker = "private-json-fixture"
	composite := `{ "credential" : "` + marker + `" }`
	r := New(Options{Values: []string{composite, `a\u0000b`}})
	for _, input := range []string{
		composite,
		`{"nested":` + composite + `,"other":"public"}`,
		`{"nested":"a\u0000b","other":"public"}`,
	} {
		result := r.Redact(input)
		if !json.Valid([]byte(result)) || strings.Contains(result, marker) ||
			strings.Contains(result, `a\u0000b`) {
			t.Fatalf("registered JSON representation was not protected: %q", result)
		}
		assertAllStreamCuts(t, r, input, result)
	}
}

func TestLiteralRegistrationProtectsNestedJSONStrings(t *testing.T) {
	const fragment = `{ "credential" : "private-json-string" }`
	r := New(Options{Values: []string{fragment}})
	input, err := json.Marshal(map[string]string{"nested": fragment, "note": "public"})
	if err != nil {
		t.Fatal(err)
	}
	got := r.Redact(string(input))
	if got != `{"nested":"***","note":"public"}` {
		t.Fatalf("JSON-encoded registered string was not masked: %s", got)
	}
}

func TestMultipleLiteralRegistrationsInEncodedKey(t *testing.T) {
	r := New(Options{Values: []string{`fixture\u002dprivate`, "known-part"}})
	input := `{"fixture\u002dprivate known-part":"ordinary","note":"public"}`
	want := `{"***":"ordinary","note":"public"}`
	if got := r.Redact(input); got != want {
		t.Fatalf("part of a registered key remained after JSON encoding: %q", got)
	}
	assertAllStreamCuts(t, r, input, want)
}
