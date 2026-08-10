package redact

import "testing"

// These unit tests cover rule classes and branches the golden corpus does
// not exercise (STANDARD_RULESET has no `header` rule, no `video-blur`/
// `origin-allow` rule, and no malformed-pointer case), so full coverage
// doesn't depend on adding fixtures upstream that don't belong to Phase 0's
// corpus.

func TestParseRuleset(t *testing.T) {
	if _, err := ParseRuleset([]byte(`not json`)); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if _, err := ParseRuleset([]byte(`{"rules":[]}`)); err == nil {
		t.Fatal("expected error for missing version")
	}
	rs, err := ParseRuleset([]byte(`{"version":"7","rules":[]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rs.Version != "7" {
		t.Fatalf("got version %q, want 7", rs.Version)
	}
}

func TestNew_InvalidPattern(t *testing.T) {
	_, err := New(Ruleset{Version: "1", Rules: []Rule{
		{ID: "bad", Class: ClassPattern, Pattern: "(unterminated"},
	}})
	if err == nil {
		t.Fatal("expected error for invalid pattern")
	}
}

func TestHeaderRule(t *testing.T) {
	engine, err := New(Ruleset{Version: "1", Rules: []Rule{
		{ID: "hdr:auth", Class: ClassHeader, Name: "Authorization"},
	}})
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}

	cases := []struct {
		name    string
		kind    string
		payload map[string]any
		want    bool
	}{
		{
			name: "matches request header case-insensitively",
			kind: "network",
			payload: map[string]any{
				"requestHeaders": map[string]any{"authorization": "Bearer xyz"},
			},
			want: true,
		},
		{
			name: "matches response header",
			kind: "network",
			payload: map[string]any{
				"responseHeaders": map[string]any{"Authorization": "Bearer xyz"},
			},
			want: true,
		},
		{
			name:    "no match when header absent",
			kind:    "network",
			payload: map[string]any{"requestHeaders": map[string]any{"X-Other": "v"}},
			want:    false,
		},
		{
			name:    "non-network kind never matches",
			kind:    "console",
			payload: map[string]any{"requestHeaders": map[string]any{"authorization": "v"}},
			want:    false,
		},
		{
			name:    "headers field wrong type is ignored",
			kind:    "network",
			payload: map[string]any{"requestHeaders": "not-a-map"},
			want:    false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := len(engine.Detect(Event{Kind: tc.kind, Payload: tc.payload})) > 0
			if got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestVideoBlurAndOriginAllow_NeverMatchAnEvent(t *testing.T) {
	engine, err := New(Ruleset{Version: "1", Rules: []Rule{
		{ID: "vb:1", Class: ClassVideoBlur, Selector: "#video"},
		{ID: "oa:1", Class: ClassOriginAllow, Origins: []string{"https://example.com"}},
	}})
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}
	got := engine.Detect(Event{Kind: "network", Payload: map[string]any{"url": "https://example.com"}})
	if len(got) != 0 {
		t.Fatalf("expected no matches, got %v", got)
	}
}

func TestUnknownRuleClass_NeverMatches(t *testing.T) {
	engine, err := New(Ruleset{Version: "1", Rules: []Rule{
		{ID: "unknown:1", Class: RuleClass("something-new")},
	}})
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}
	got := engine.Detect(Event{Kind: "network", Payload: map[string]any{}})
	if len(got) != 0 {
		t.Fatalf("expected no matches, got %v", got)
	}
}

func TestFieldPath_EdgeCases(t *testing.T) {
	engine, err := New(Ruleset{Version: "1", Rules: []Rule{
		{ID: "fp:root", Class: ClassFieldPath, Pointer: ""},
	}})
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}

	t.Run("root pointer matches any JSON body", func(t *testing.T) {
		got := engine.Detect(Event{Kind: "network", Payload: map[string]any{
			"requestBody": `{"a":1}`,
		}})
		if len(got) == 0 {
			t.Fatal("expected root pointer to match")
		}
	})

	t.Run("non-JSON body never matches", func(t *testing.T) {
		got := engine.Detect(Event{Kind: "network", Payload: map[string]any{
			"requestBody": `not json`,
		}})
		if len(got) != 0 {
			t.Fatalf("expected no match, got %v", got)
		}
	})

	t.Run("null body is skipped", func(t *testing.T) {
		got := engine.Detect(Event{Kind: "network", Payload: map[string]any{
			"requestBody": nil,
		}})
		if len(got) != 0 {
			t.Fatalf("expected no match, got %v", got)
		}
	})

	t.Run("non-network kind never matches", func(t *testing.T) {
		got := engine.Detect(Event{Kind: "console", Payload: map[string]any{
			"requestBody": `{"a":1}`,
		}})
		if len(got) != 0 {
			t.Fatalf("expected no match, got %v", got)
		}
	})

	deep, err := New(Ruleset{Version: "1", Rules: []Rule{
		{ID: "fp:deep", Class: ClassFieldPath, Pointer: "/patient/*/nik"},
	}})
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}

	t.Run("wildcard over array matches", func(t *testing.T) {
		got := deep.Detect(Event{Kind: "network", Payload: map[string]any{
			"requestBody": `{"patient":[{"nik":"123"}]}`,
		}})
		if len(got) == 0 {
			t.Fatal("expected wildcard-over-array to match")
		}
	})

	t.Run("wildcard over object matches", func(t *testing.T) {
		got := deep.Detect(Event{Kind: "network", Payload: map[string]any{
			"requestBody": `{"patient":{"a":{"nik":"123"}}}`,
		}})
		if len(got) == 0 {
			t.Fatal("expected wildcard-over-object to match")
		}
	})

	t.Run("wildcard over array with no matching item does not match", func(t *testing.T) {
		got := deep.Detect(Event{Kind: "network", Payload: map[string]any{
			"requestBody": `{"patient":[{"other":"x"},{"other":"y"}]}`,
		}})
		if len(got) != 0 {
			t.Fatalf("expected no match, got %v", got)
		}
	})

	t.Run("wildcard over object with no matching item does not match", func(t *testing.T) {
		got := deep.Detect(Event{Kind: "network", Payload: map[string]any{
			"requestBody": `{"patient":{"a":{"other":"x"}}}`,
		}})
		if len(got) != 0 {
			t.Fatalf("expected no match, got %v", got)
		}
	})

	t.Run("wildcard over non-container does not match", func(t *testing.T) {
		got := deep.Detect(Event{Kind: "network", Payload: map[string]any{
			"requestBody": `{"patient":"not-a-container"}`,
		}})
		if len(got) != 0 {
			t.Fatalf("expected no match, got %v", got)
		}
	})

	t.Run("valid array index resolves", func(t *testing.T) {
		idxEngine, err := New(Ruleset{Version: "1", Rules: []Rule{
			{ID: "fp:idx", Class: ClassFieldPath, Pointer: "/items/1"},
		}})
		if err != nil {
			t.Fatalf("build engine: %v", err)
		}
		got := idxEngine.Detect(Event{Kind: "network", Payload: map[string]any{
			"requestBody": `{"items":[1,2]}`,
		}})
		if len(got) == 0 {
			t.Fatal("expected match at valid array index")
		}
	})

	t.Run("array index out of range does not match", func(t *testing.T) {
		idxEngine, err := New(Ruleset{Version: "1", Rules: []Rule{
			{ID: "fp:idx", Class: ClassFieldPath, Pointer: "/items/5"},
		}})
		if err != nil {
			t.Fatalf("build engine: %v", err)
		}
		got := idxEngine.Detect(Event{Kind: "network", Payload: map[string]any{
			"requestBody": `{"items":[1,2]}`,
		}})
		if len(got) != 0 {
			t.Fatalf("expected no match, got %v", got)
		}
	})

	t.Run("non-numeric array segment does not match", func(t *testing.T) {
		idxEngine, err := New(Ruleset{Version: "1", Rules: []Rule{
			{ID: "fp:idx", Class: ClassFieldPath, Pointer: "/items/foo"},
		}})
		if err != nil {
			t.Fatalf("build engine: %v", err)
		}
		got := idxEngine.Detect(Event{Kind: "network", Payload: map[string]any{
			"requestBody": `{"items":[1,2]}`,
		}})
		if len(got) != 0 {
			t.Fatalf("expected no match, got %v", got)
		}
	})

	t.Run("missing object key does not match", func(t *testing.T) {
		missing, err := New(Ruleset{Version: "1", Rules: []Rule{
			{ID: "fp:missing", Class: ClassFieldPath, Pointer: "/nope"},
		}})
		if err != nil {
			t.Fatalf("build engine: %v", err)
		}
		got := missing.Detect(Event{Kind: "network", Payload: map[string]any{
			"requestBody": `{"present":1}`,
		}})
		if len(got) != 0 {
			t.Fatalf("expected no match, got %v", got)
		}
	})

	t.Run("pointer through a scalar does not match", func(t *testing.T) {
		scalar, err := New(Ruleset{Version: "1", Rules: []Rule{
			{ID: "fp:scalar", Class: ClassFieldPath, Pointer: "/a/b"},
		}})
		if err != nil {
			t.Fatalf("build engine: %v", err)
		}
		got := scalar.Detect(Event{Kind: "network", Payload: map[string]any{
			"requestBody": `{"a":1}`,
		}})
		if len(got) != 0 {
			t.Fatalf("expected no match, got %v", got)
		}
	})

	t.Run("empty array-index segment does not match", func(t *testing.T) {
		emptyIdx, err := New(Ruleset{Version: "1", Rules: []Rule{
			{ID: "fp:emptyidx", Class: ClassFieldPath, Pointer: "/items/"},
		}})
		if err != nil {
			t.Fatalf("build engine: %v", err)
		}
		got := emptyIdx.Detect(Event{Kind: "network", Payload: map[string]any{
			"requestBody": `{"items":[1,2]}`,
		}})
		if len(got) != 0 {
			t.Fatalf("expected no match, got %v", got)
		}
	})

	t.Run("escaped pointer segments (~0 ~1) decode", func(t *testing.T) {
		esc, err := New(Ruleset{Version: "1", Rules: []Rule{
			{ID: "fp:esc", Class: ClassFieldPath, Pointer: "/a~1b"},
		}})
		if err != nil {
			t.Fatalf("build engine: %v", err)
		}
		got := esc.Detect(Event{Kind: "network", Payload: map[string]any{
			"requestBody": `{"a/b":1}`,
		}})
		if len(got) == 0 {
			t.Fatal("expected escaped segment to resolve")
		}
	})
}

func TestDomSelector_EdgeCases(t *testing.T) {
	engine, err := New(Ruleset{Version: "1", Rules: []Rule{
		{ID: "ds:1", Class: ClassDomSelector, Selector: "#name"},
	}})
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}

	if got := engine.Detect(Event{Kind: "network", Payload: map[string]any{"targetSelector": "#name"}}); len(got) != 0 {
		t.Fatalf("expected no match for non-interaction kind, got %v", got)
	}
	if got := engine.Detect(Event{Kind: "interaction", Payload: map[string]any{"targetSelector": "#other"}}); len(got) != 0 {
		t.Fatalf("expected no match for different selector, got %v", got)
	}
	if got := engine.Detect(Event{Kind: "interaction", Payload: map[string]any{}}); len(got) != 0 {
		t.Fatalf("expected no match when targetSelector absent, got %v", got)
	}
}

func TestPattern_NonJSONBodyAndOtherKinds(t *testing.T) {
	engine, err := New(Ruleset{Version: "1", Rules: []Rule{PHIPatterns[1]}}) // builtin:nik
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}

	t.Run("matches inside non-JSON body string", func(t *testing.T) {
		got := engine.Detect(Event{Kind: "network", Payload: map[string]any{
			"requestBody": "contact nik 1234567890123456 now",
		}})
		if len(got) == 0 {
			t.Fatal("expected match in non-JSON body")
		}
	})

	t.Run("matches in non-network payload value", func(t *testing.T) {
		got := engine.Detect(Event{Kind: "annotation", Payload: map[string]any{
			"text": "nik 1234567890123456",
		}})
		if len(got) == 0 {
			t.Fatal("expected match in annotation text")
		}
	})

	t.Run("no match when absent", func(t *testing.T) {
		got := engine.Detect(Event{Kind: "annotation", Payload: map[string]any{
			"text": "nothing to see here",
		}})
		if len(got) != 0 {
			t.Fatalf("expected no match, got %v", got)
		}
	})
}

func TestVersion(t *testing.T) {
	engine, err := New(Ruleset{Version: "42", Rules: nil})
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}
	if engine.Version() != "42" {
		t.Fatalf("got %q, want 42", engine.Version())
	}
}
