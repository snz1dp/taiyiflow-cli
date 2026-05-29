package computerbridge

import "testing"

func TestResolveCoordinatePairRequiresCompletePair(t *testing.T) {
	if _, _, _, err := resolveCoordinatePair(map[string]any{"x": 12}, "x", "y", true); err == nil {
		t.Fatal("expected incomplete coordinate pair to fail")
	}
	if _, _, _, err := resolveCoordinatePair(map[string]any{"x": "12", "y": "abc"}, "x", "y", true); err == nil {
		t.Fatal("expected invalid integer coordinate to fail")
	}
	x, y, hasPair, err := resolveCoordinatePair(map[string]any{"x": "12", "y": 34}, "x", "y", true)
	if err != nil {
		t.Fatalf("expected valid coordinate pair, got %v", err)
	}
	if !hasPair || x != 12 || y != 34 {
		t.Fatalf("unexpected resolved coordinate pair: x=%d y=%d hasPair=%t", x, y, hasPair)
	}
}

func TestResolveCoordinatePairAllowsEmptyOptionalPair(t *testing.T) {
	x, y, hasPair, err := resolveCoordinatePair(map[string]any{}, "x", "y", false)
	if err != nil {
		t.Fatalf("expected optional empty pair to pass, got %v", err)
	}
	if hasPair || x != 0 || y != 0 {
		t.Fatalf("expected no coordinate pair, got x=%d y=%d hasPair=%t", x, y, hasPair)
	}
}

func TestResolveMouseButtonSupportsAliases(t *testing.T) {
	code, label, err := resolveMouseButton(map[string]any{"button": "right"})
	if err != nil {
		t.Fatalf("expected right button to be supported, got %v", err)
	}
	if code != 1 || label != "right" {
		t.Fatalf("unexpected right button mapping: code=%d label=%s", code, label)
	}
	code, label, err = resolveMouseButton(map[string]any{"button": "center"})
	if err != nil {
		t.Fatalf("expected center alias to be supported, got %v", err)
	}
	if code != 2 || label != "middle" {
		t.Fatalf("unexpected middle button mapping: code=%d label=%s", code, label)
	}
	if _, _, err := resolveMouseButton(map[string]any{"button": "wheel"}); err == nil {
		t.Fatal("expected unsupported mouse button to fail")
	}
}

func TestClampAndInterpolateInt(t *testing.T) {
	if got := clampInt(10, 1, 5); got != 5 {
		t.Fatalf("expected clamp to upper bound, got %d", got)
	}
	if got := clampInt(-1, 0, 5); got != 0 {
		t.Fatalf("expected clamp to lower bound, got %d", got)
	}
	if got := interpolateInt(10, 20, 1, 2); got != 15 {
		t.Fatalf("expected interpolated midpoint, got %d", got)
	}
	if got := interpolateInt(10, 20, 1, 1); got != 20 {
		t.Fatalf("expected single-step interpolation to jump to target, got %d", got)
	}
}