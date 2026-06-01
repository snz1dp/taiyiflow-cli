package computerbridge

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

type mouseEventCall struct {
	kind       string
	eventKind  int
	button     int
	x          int
	y          int
	clickState int
}

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

func TestTranslateScreenPointUsesExplicitScreenshotDimensions(t *testing.T) {
	layouts := []displayLayout{{
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
	}}
	point, err := translateScreenPoint(map[string]any{
		"display_id":          "1",
		"screenshot_width_px": 3200,
		"screenshot_height_px": 2080,
	}, 1600, 260, layouts)
	if err != nil {
		t.Fatalf("expected translation with explicit screenshot dimensions to succeed, got %v", err)
	}
	if point.X != 960 || point.Y != -728 {
		t.Fatalf("unexpected point with explicit screenshot dimensions: %+v", point)
	}
}

func TestTranslateScreenPointUsesCachedScreenshotDimensions(t *testing.T) {
	clearDisplayScreenshotDimensionsCache()
	t.Cleanup(clearDisplayScreenshotDimensionsCache)
	cacheDisplayScreenshotDimensions("1", 1, 3200, 2080)
	layouts := []displayLayout{{
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
	}}
	point, err := translateScreenPoint(map[string]any{"display_id": "1"}, 1600, 260, layouts)
	if err != nil {
		t.Fatalf("expected translation with cached screenshot dimensions to succeed, got %v", err)
	}
	if point.X != 960 || point.Y != -728 {
		t.Fatalf("unexpected point with cached screenshot dimensions: %+v", point)
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

func TestDarwinMouseClickMovesBeforeClickingSpecifiedCoordinates(t *testing.T) {
	originalCurrentMousePositionFunc := darwinCurrentMousePositionFunc
	originalPostMouseMoveFunc := darwinPostMouseMoveFunc
	originalPostMouseButtonEventFunc := darwinPostMouseButtonEventFunc
	originalSettleMouseBeforeClick := darwinSettleMouseBeforeClick
	t.Cleanup(func() {
		darwinCurrentMousePositionFunc = originalCurrentMousePositionFunc
		darwinPostMouseMoveFunc = originalPostMouseMoveFunc
		darwinPostMouseButtonEventFunc = originalPostMouseButtonEventFunc
		darwinSettleMouseBeforeClick = originalSettleMouseBeforeClick
	})

	darwinCurrentMousePositionFunc = func() (int, int, error) {
		t.Fatal("specified-coordinate click should not read current pointer position")
		return 0, 0, nil
	}
	events := []mouseEventCall{}
	darwinPostMouseMoveFunc = func(x int, y int) error {
		events = append(events, mouseEventCall{kind: "move", x: x, y: y})
		return nil
	}
	settled := false
	darwinSettleMouseBeforeClick = func(ctx context.Context) error {
		settled = true
		return nil
	}
	darwinPostMouseButtonEventFunc = func(eventKind int, button int, x int, y int, clickState int) error {
		if !settled {
			t.Fatal("expected click to wait for pointer settle after explicit move")
		}
		events = append(events, mouseEventCall{kind: "button", eventKind: eventKind, button: button, x: x, y: y, clickState: clickState})
		return nil
	}

	result, err := darwinMouseClick(context.Background(), map[string]any{
		"x":           321,
		"y":           654,
		"button":      "left",
		"click_count": 2,
	})
	if err != nil {
		t.Fatalf("expected click with explicit coordinates to succeed, got %v", err)
	}
	wantEvents := []mouseEventCall{
		{kind: "move", x: 321, y: 654},
		{kind: "button", eventKind: mouseEventDown, button: 0, x: 321, y: 654, clickState: 1},
		{kind: "button", eventKind: mouseEventUp, button: 0, x: 321, y: 654, clickState: 1},
		{kind: "button", eventKind: mouseEventDown, button: 0, x: 321, y: 654, clickState: 2},
		{kind: "button", eventKind: mouseEventUp, button: 0, x: 321, y: 654, clickState: 2},
	}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("unexpected darwin click event sequence: got %#v want %#v", events, wantEvents)
	}
	if intValue(result["x"], 0) != 321 || intValue(result["y"], 0) != 654 {
		t.Fatalf("expected click result to report target point, got %#v", result)
	}
	if stringValue(result["coordinate_space"]) != "global_screen" || stringValue(result["input_coordinate_space"]) != "global_screen" {
		t.Fatalf("expected click result to preserve global coordinate metadata, got %#v", result)
	}
}

func TestDarwinMouseClickUsesCurrentPointerWhenCoordinatesOmitted(t *testing.T) {
	originalCurrentMousePositionFunc := darwinCurrentMousePositionFunc
	originalPostMouseMoveFunc := darwinPostMouseMoveFunc
	originalPostMouseButtonEventFunc := darwinPostMouseButtonEventFunc
	originalSettleMouseBeforeClick := darwinSettleMouseBeforeClick
	t.Cleanup(func() {
		darwinCurrentMousePositionFunc = originalCurrentMousePositionFunc
		darwinPostMouseMoveFunc = originalPostMouseMoveFunc
		darwinPostMouseButtonEventFunc = originalPostMouseButtonEventFunc
		darwinSettleMouseBeforeClick = originalSettleMouseBeforeClick
	})

	darwinCurrentMousePositionFunc = func() (int, int, error) {
		return 77, 88, nil
	}
	darwinPostMouseMoveFunc = func(x int, y int) error {
		t.Fatalf("omitted-coordinate click should not post an extra move, got (%d,%d)", x, y)
		return nil
	}
	darwinSettleMouseBeforeClick = func(ctx context.Context) error {
		t.Fatal("omitted-coordinate click should not wait for settle without an explicit move")
		return nil
	}
	events := []mouseEventCall{}
	darwinPostMouseButtonEventFunc = func(eventKind int, button int, x int, y int, clickState int) error {
		events = append(events, mouseEventCall{kind: "button", eventKind: eventKind, button: button, x: x, y: y, clickState: clickState})
		return nil
	}

	result, err := darwinMouseClick(context.Background(), map[string]any{"button": "right"})
	if err != nil {
		t.Fatalf("expected click without explicit coordinates to succeed, got %v", err)
	}
	wantEvents := []mouseEventCall{
		{kind: "button", eventKind: mouseEventDown, button: 1, x: 77, y: 88, clickState: 1},
		{kind: "button", eventKind: mouseEventUp, button: 1, x: 77, y: 88, clickState: 1},
	}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("unexpected darwin click event sequence without coordinates: got %#v want %#v", events, wantEvents)
	}
	if intValue(result["x"], 0) != 77 || intValue(result["y"], 0) != 88 || stringValue(result["button"]) != "right" {
		t.Fatalf("unexpected click result without coordinates: %#v", result)
	}
}

func TestDarwinKeyPressFallsBackToTypeTextForNonASCII(t *testing.T) {
	originalDarwinTypeTextFunc := darwinTypeTextFunc
	t.Cleanup(func() {
		darwinTypeTextFunc = originalDarwinTypeTextFunc
	})
	called := false
	var capturedArguments map[string]any
	darwinTypeTextFunc = func(ctx context.Context, arguments map[string]any) (map[string]any, error) {
		called = true
		capturedArguments = map[string]any{}
		for key, value := range arguments {
			capturedArguments[key] = value
		}
		return map[string]any{"platform": "darwin"}, nil
	}

	result, err := darwinKeyPress(context.Background(), map[string]any{"key": "老", "repeat": 2})
	if err != nil {
		t.Fatalf("expected non-ASCII key_press to fall back to type_text, got %v", err)
	}
	if !called {
		t.Fatal("expected non-ASCII key_press to call type_text fallback")
	}
	if stringValue(capturedArguments["text"]) != "老老" {
		t.Fatalf("expected repeated text to be forwarded to type_text, got %#v", capturedArguments)
	}
	if stringValue(result["fallback_mode"]) != "type_text" || intValue(result["text_length"], 0) != 2 {
		t.Fatalf("expected fallback result metadata, got %#v", result)
	}
}

func TestDarwinKeyPressRejectsNonASCIIWithModifiers(t *testing.T) {
	originalDarwinTypeTextFunc := darwinTypeTextFunc
	t.Cleanup(func() {
		darwinTypeTextFunc = originalDarwinTypeTextFunc
	})
	darwinTypeTextFunc = func(ctx context.Context, arguments map[string]any) (map[string]any, error) {
		t.Fatalf("expected modified non-ASCII key_press to fail before type_text fallback, got %#v", arguments)
		return nil, nil
	}

	_, err := darwinKeyPress(context.Background(), map[string]any{"key": "老", "modifiers": []any{"command"}})
	if err == nil || !strings.Contains(err.Error(), "请改用 type_text") {
		t.Fatalf("expected modified non-ASCII key_press to fail with guidance, got %v", err)
	}
}

func TestDarwinKeyPressFallsBackToTypeTextForTextLikeKey(t *testing.T) {
	originalDarwinTypeTextFunc := darwinTypeTextFunc
	t.Cleanup(func() {
		darwinTypeTextFunc = originalDarwinTypeTextFunc
	})
	called := false
	var capturedText string
	darwinTypeTextFunc = func(ctx context.Context, arguments map[string]any) (map[string]any, error) {
		called = true
		capturedText = stringValue(arguments["text"])
		return map[string]any{"platform": "darwin"}, nil
	}

	result, err := darwinKeyPress(context.Background(), map[string]any{"key": "老婆A"})
	if err != nil {
		t.Fatalf("expected text-like key_press to fall back to type_text, got %v", err)
	}
	if !called {
		t.Fatal("expected text-like key_press to call type_text fallback")
	}
	if capturedText != "老婆A" {
		t.Fatalf("expected full key string to be forwarded to type_text, got %q", capturedText)
	}
	if stringValue(result["fallback_mode"]) != "type_text" || intValue(result["text_length"], 0) != 3 {
		t.Fatalf("expected text fallback metadata, got %#v", result)
	}
}

func TestDarwinKeyPressKeepsNamedSpecialKeys(t *testing.T) {
	if text, fallback, err := darwinKeyPressTextFallback("enter", "", 1); err != nil {
		t.Fatalf("expected named special key to bypass text fallback, got err=%v", err)
	} else if fallback || text != "" {
		t.Fatalf("expected named special key to avoid text fallback, got text=%q fallback=%t", text, fallback)
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

func TestShouldDarwinUseClipboardTextInput(t *testing.T) {
	if !shouldDarwinUseClipboardTextInput("hello", false) {
		t.Fatal("expected ASCII text to use clipboard path when Unicode input is unavailable")
	}
	if shouldDarwinUseClipboardTextInput("hello", true) {
		t.Fatal("expected ASCII text to stay on direct input path when Unicode input is available")
	}
	if !shouldDarwinUseClipboardTextInput("老婆A", true) {
		t.Fatal("expected mixed Chinese text to use clipboard path")
	}
	if !shouldDarwinUseClipboardTextInput("老婆A", false) {
		t.Fatal("expected mixed Chinese text to keep using clipboard path when Unicode input is unavailable")
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
