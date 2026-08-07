package policy

import (
	"reflect"
	"testing"
)

func ptrInt64(v int64) *int64 { return &v }
func ptrInt(v int) *int       { return &v }
func ptrBool(v bool) *bool    { return &v }

func TestResolveNoOverrides(t *testing.T) {
	got := Resolve(Defaults, Overrides{})
	if !reflect.DeepEqual(got, Defaults) {
		t.Errorf("Resolve with no overrides = %+v, want defaults %+v", got, Defaults)
	}
}

func TestResolvePartialOverride(t *testing.T) {
	got := Resolve(Defaults, Overrides{RetentionDays: ptrInt(30)})

	want := Defaults
	want.RetentionDays = 30
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve with partial override = %+v, want %+v", got, want)
	}
}

func TestResolveFullOverride(t *testing.T) {
	o := Overrides{
		StorageCapBytes:  ptrInt64(1024),
		CaptureCap:       ptrInt(5),
		LLMProviderChain: []string{"anthropic"},
		LocalOnly:        ptrBool(false),
		OriginAllowList:  []string{"https://example.com"},
		RetentionDays:    ptrInt(7),
	}
	got := Resolve(Defaults, o)

	want := Policy{
		StorageCapBytes:  1024,
		CaptureCap:       5,
		LLMProviderChain: []string{"anthropic"},
		LocalOnly:        false,
		OriginAllowList:  []string{"https://example.com"},
		RetentionDays:    7,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve with full override = %+v, want %+v", got, want)
	}
}

func TestValidate_NoRetentionDaysIsRejected(t *testing.T) {
	// No override supplied: Resolve leaves RetentionDays at Defaults'
	// (deliberately absent) zero value, and Validate must reject that —
	// a workspace cannot come into existence on an implicit number nobody
	// chose (spec §22 open question 4).
	got := Resolve(Defaults, Overrides{})
	if err := got.Validate(); err == nil {
		t.Fatal("Validate() with no retentionDays override = nil, want ErrRetentionRequired")
	} else if err != ErrRetentionRequired {
		t.Fatalf("Validate() error = %v, want ErrRetentionRequired", err)
	}
}

func TestValidate_NonPositiveRetentionDaysIsRejected(t *testing.T) {
	for _, days := range []int{0, -1, -90} {
		got := Resolve(Defaults, Overrides{RetentionDays: ptrInt(days)})
		if err := got.Validate(); err == nil {
			t.Fatalf("Validate() with retentionDays=%d = nil, want error", days)
		}
	}
}

func TestValidate_PositiveRetentionDaysIsAccepted(t *testing.T) {
	got := Resolve(Defaults, Overrides{RetentionDays: ptrInt(30)})
	if err := got.Validate(); err != nil {
		t.Fatalf("Validate() with retentionDays=30 = %v, want nil", err)
	}
}

func TestDefaultsMatchCaptureCoreBudget(t *testing.T) {
	// Mirrors packages/capture-core/src/storage/budget.ts's
	// DEFAULT_BYTE_LIMIT/DEFAULT_CAPTURE_CAP.
	if Defaults.StorageCapBytes != 2*1024*1024*1024 {
		t.Errorf("StorageCapBytes = %d, want 2GB", Defaults.StorageCapBytes)
	}
	if Defaults.CaptureCap != 40 {
		t.Errorf("CaptureCap = %d, want 40", Defaults.CaptureCap)
	}
	if !Defaults.LocalOnly {
		t.Error("LocalOnly default should be true (privacy-by-default)")
	}
	if Defaults.RetentionDays != 0 {
		t.Errorf("RetentionDays default = %d, want 0 (no code-level default; Validate rejects it)", Defaults.RetentionDays)
	}
}
