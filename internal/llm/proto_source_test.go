package llm

import "testing"

func TestSource_String(t *testing.T) {
	cases := map[Source]string{
		SourceUnknown:  "unknown",
		SourceSeed:     "seed",
		SourceCatalog:  "catalog",
		SourceProvider: "provider",
		SourceProbe:    "probe",
		SourceOpencode: "opencode",
		SourceExternal: "external",
		SourceUser:     "user",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("Source(%d).String() = %q, want %q", s, got, want)
		}
	}
}

func TestSource_ParseRoundTrip(t *testing.T) {
	// SourceUnknown ("unknown") is intentionally NOT
	// parseable — there's no scenario where reading it
	// back from disk is meaningful (it means "not set").
	for s := SourceSeed; s <= SourceUser; s++ {
		got, ok := ParseSource(s.String())
		if !ok {
			t.Errorf("ParseSource(%q) ok = false", s.String())
		}
		if got != s {
			t.Errorf("ParseSource(%q) = %d, want %d", s.String(), got, s)
		}
	}
}

func TestSource_ParseUnknown(t *testing.T) {
	got, ok := ParseSource("nonsense")
	if ok {
		t.Error("expected ok = false for nonsense")
	}
	if got != SourceUnknown {
		t.Errorf("got %d, want SourceUnknown", got)
	}
}

func TestSource_ParseCaseInsensitive(t *testing.T) {
	cases := []string{"SEED", "Seed", "sEeD", "  seed  "}
	for _, c := range cases {
		got, ok := ParseSource(c)
		if !ok || got != SourceSeed {
			t.Errorf("ParseSource(%q) = %d, %v", c, got, ok)
		}
	}
}

func TestSource_Overrides(t *testing.T) {
	// Probe overrides provider, catalog, seed.
	if !SourceProbe.Overrides(SourceProvider) {
		t.Error("probe should override provider")
	}
	if !SourceProbe.Overrides(SourceSeed) {
		t.Error("probe should override seed")
	}
	// Seed does not override anything.
	if SourceSeed.Overrides(SourceCatalog) {
		t.Error("seed should not override catalog")
	}
	// Same source does not override itself.
	if SourceProvider.Overrides(SourceProvider) {
		t.Error("same source should not override")
	}
	// Catalog overrides seed.
	if !SourceCatalog.Overrides(SourceSeed) {
		t.Error("catalog should override seed")
	}
}

func TestSource_PriorityOrder(t *testing.T) {
	// Documented order: seed < catalog < provider < probe < opencode < external < user.
	if !(SourceSeed < SourceCatalog &&
		SourceCatalog < SourceProvider &&
		SourceProvider < SourceProbe &&
		SourceProbe < SourceOpencode &&
		SourceOpencode < SourceExternal &&
		SourceExternal < SourceUser) {
		t.Error("priority order is wrong")
	}
}

func TestSource_UserOverridesAll(t *testing.T) {
	sources := []Source{SourceSeed, SourceCatalog, SourceProvider, SourceProbe, SourceOpencode, SourceExternal}
	for _, s := range sources {
		if !SourceUser.Overrides(s) {
			t.Errorf("user should override %s", s)
		}
	}
}

func TestSource_ExternalOverridesSeedAndCatalog(t *testing.T) {
	if !SourceExternal.Overrides(SourceSeed) {
		t.Error("external should override seed")
	}
	if !SourceExternal.Overrides(SourceCatalog) {
		t.Error("external should override catalog")
	}
	if SourceExternal.Overrides(SourceUser) {
		t.Error("external should not override user")
	}
}
