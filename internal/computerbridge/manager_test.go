package computerbridge

import (
	"strings"
	"testing"
)

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

func TestTranslateScreenPointUsesDisplayIndexAsLocalOrigin(t *testing.T) {
	layouts := []displayLayout{
		{DisplayID: "101", DisplayIndex: 1, OriginX: 0, OriginY: 0, Width: 1920, Height: 1080, Primary: true},
		{DisplayID: "202", DisplayIndex: 2, OriginX: 1920, OriginY: -120, Width: 2560, Height: 1440},
	}
	point, err := translateScreenPoint(map[string]any{"display_index": 2}, 40, 60, layouts)
	if err != nil {
		t.Fatalf("expected display-local translation to succeed, got %v", err)
	}
	if point.X != 1960 || point.Y != -60 {
		t.Fatalf("unexpected translated point: %+v", point)
	}
	if point.InputCoordinateSpace != "display_local" || !point.HasDisplay || point.Display.DisplayIndex != 2 {
		t.Fatalf("unexpected point metadata: %+v", point)
	}
}

func TestTranslateScreenPointScalesDisplayLocalScreenshotPixels(t *testing.T) {
	layouts := []displayLayout{
		{
			DisplayID:    "1",
			DisplayIndex: 1,
			OriginX:      320,
			OriginY:      -832,
			Width:        1280,
			Height:       832,
			PixelWidth:   2560,
			PixelHeight:  1664,
			ScaleFactor:  2.0,
			Primary:      true,
		},
	}
	point, err := translateScreenPoint(map[string]any{"display_id": "1"}, 640, 400, layouts)
	if err != nil {
		t.Fatalf("expected scaled display-local translation to succeed, got %v", err)
	}
	if point.X != 640 || point.Y != -632 {
		t.Fatalf("unexpected scaled translated point: %+v", point)
	}
	if point.InputCoordinateSpace != "display_local" || !point.HasDisplay || point.Display.ScaleFactor != 2.0 {
		t.Fatalf("unexpected scaled point metadata: %+v", point)
	}
}

func TestTranslateScreenPointRespectsExplicitGlobalSpace(t *testing.T) {
	layouts := []displayLayout{{DisplayID: "202", DisplayIndex: 2, OriginX: 1920, OriginY: -120, Width: 2560, Height: 1440}}
	point, err := translateScreenPoint(map[string]any{"display_index": 2, "coordinate_space": "global_screen"}, 40, 60, layouts)
	if err != nil {
		t.Fatalf("expected explicit global coordinate space to pass, got %v", err)
	}
	if point.X != 40 || point.Y != 60 || point.HasDisplay {
		t.Fatalf("expected global coordinates to stay unchanged, got %+v", point)
	}
}

func TestTranslateScreenPointRejectsUnknownDisplay(t *testing.T) {
	if _, err := translateScreenPoint(map[string]any{"display_index": 3}, 10, 20, []displayLayout{{DisplayID: "101", DisplayIndex: 1}}); err == nil {
		t.Fatal("expected unknown display to fail")
	}
}

func TestApplyDisplayLayoutOverridesDisplayIndexAndOrigin(t *testing.T) {
	item := map[string]any{"display_id": "2", "display_index": 99, "primary": false}
	applyDisplayLayout(item, displayLayout{DisplayID: "202", DisplayIndex: 2, OriginX: 1920, OriginY: -120, Primary: true})
	if intValue(item["display_index"], 0) != 2 || intValue(item["origin_x"], 0) != 1920 || intValue(item["origin_y"], 0) != -120 {
		t.Fatalf("unexpected layout merge result: %#v", item)
	}
	if !boolValue(item["primary"]) || stringValue(item["coordinate_space"]) != "global_screen" {
		t.Fatalf("expected primary/global metadata after merge, got %#v", item)
	}
}

func TestBuildDisplaysContextUsesLayoutOriginAndPlatform(t *testing.T) {
	result := buildDisplaysContext("linux", []displayLayout{{DisplayID: "HDMI-1", DisplayIndex: 2, OriginX: 1920, OriginY: -80, Width: 2560, Height: 1440, Primary: true}})
	if stringValue(result["platform"]) != "linux" || stringValue(result["coordinate_space"]) != "global_screen" {
		t.Fatalf("unexpected displays_context envelope: %#v", result)
	}
	displays, ok := result["displays"].([]map[string]any)
	if !ok || len(displays) != 1 {
		t.Fatalf("expected one display item, got %#v", result)
	}
	item := displays[0]
	if intValue(item["display_index"], 0) != 2 || intValue(item["origin_x"], 0) != 1920 || intValue(item["origin_y"], 0) != -80 {
		t.Fatalf("expected origin and display index in displays_context, got %#v", item)
	}
	if stringValue(item["name"]) != "HDMI-1" || !boolValue(item["primary"]) {
		t.Fatalf("expected display identity to be preserved, got %#v", item)
	}
}

func TestBuildDisplaysContextUsesPixelAndLogicalSizes(t *testing.T) {
	result := buildDisplaysContext("darwin", []displayLayout{{
		DisplayID:    "1",
		DisplayIndex: 1,
		OriginX:      320,
		OriginY:      -832,
		Width:        1280,
		Height:       832,
		PixelWidth:   2560,
		PixelHeight:  1664,
		ScaleFactor:  2.0,
		Primary:      true,
	}})
	displays, ok := result["displays"].([]map[string]any)
	if !ok || len(displays) != 1 {
		t.Fatalf("expected one display item, got %#v", result)
	}
	item := displays[0]
	if intValue(item["width_px"], 0) != 2560 || intValue(item["height_px"], 0) != 1664 {
		t.Fatalf("expected physical pixel size in displays_context, got %#v", item)
	}
	if intValue(item["logical_width"], 0) != 1280 || intValue(item["logical_height"], 0) != 832 {
		t.Fatalf("expected logical display size in displays_context, got %#v", item)
	}
	if got := item["scale_factor"].(float64); got != 2.0 {
		t.Fatalf("expected scale_factor 2.0, got %#v", item)
	}
	if intValue(item["origin_x"], 0) != 320 || intValue(item["origin_y"], 0) != -832 {
		t.Fatalf("expected display origin in displays_context, got %#v", item)
	}
}

func TestAppleScriptStringEscapesQuoteAndBackslash(t *testing.T) {
	got := appleScriptString(`a"b\c`)
	if got != `"a\"b\\c"` {
		t.Fatalf("unexpected AppleScript string escaping: %q", got)
	}
}

func TestBuildDarwinTypeTextScriptPreservesLineBreaksAndTrailingEnter(t *testing.T) {
	script := buildDarwinTypeTextScript("第一行\n\n第二行", true)
	if !strings.Contains(script, `keystroke "第一行"`) {
		t.Fatalf("expected first line keystroke in script, got %q", script)
	}
	if !strings.Contains(script, `keystroke "第二行"`) {
		t.Fatalf("expected second line keystroke in script, got %q", script)
	}
	if got := strings.Count(script, "key code 36"); got != 3 {
		t.Fatalf("expected three enter key codes for blank line + trailing enter, got %d in %q", got, script)
	}
}

func TestRequiresDarwinClipboardTextInput(t *testing.T) {
	if requiresDarwinClipboardTextInput("hello") {
		t.Fatal("expected ASCII text to stay on direct input path")
	}
	if !requiresDarwinClipboardTextInput("老婆") {
		t.Fatal("expected Chinese text to use clipboard paste fallback")
	}
}

func TestBuildDarwinPasteShortcutScriptUsesCommandVWithDelay(t *testing.T) {
	script := buildDarwinPasteShortcutScript()
	if !strings.Contains(script, "key code 9 using command down") {
		t.Fatalf("expected paste shortcut script to send Command+V, got %q", script)
	}
	if !strings.Contains(script, "delay 0.05") {
		t.Fatalf("expected paste shortcut script to wait briefly before clipboard restore, got %q", script)
	}
}
