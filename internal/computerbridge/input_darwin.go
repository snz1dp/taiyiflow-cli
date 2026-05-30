//go:build darwin && cgo

package computerbridge

/*
#cgo LDFLAGS: -framework ApplicationServices
#include <ApplicationServices/ApplicationServices.h>
#include <stdint.h>

static int currentMouseLocation(double* x, double* y) {
	CGEventRef event = CGEventCreate(NULL);
	if (event == NULL) {
		return 1;
	}
	CGPoint point = CGEventGetLocation(event);
	CFRelease(event);
	*x = point.x;
	*y = point.y;
	return 0;
}

static int postMouseMove(double x, double y) {
	CGEventRef event = CGEventCreateMouseEvent(NULL, kCGEventMouseMoved, CGPointMake(x, y), kCGMouseButtonLeft);
	if (event == NULL) {
		return 1;
	}
	CGEventPost(kCGHIDEventTap, event);
	CFRelease(event);
	return 0;
}

static int postMouseButtonEvent(int kind, int button, double x, double y, long clickState) {
	CGEventType eventType;
	switch (kind) {
		case 0:
			if (button == 1) eventType = kCGEventRightMouseDown;
			else if (button == 2) eventType = kCGEventOtherMouseDown;
			else eventType = kCGEventLeftMouseDown;
			break;
		case 1:
			if (button == 1) eventType = kCGEventRightMouseUp;
			else if (button == 2) eventType = kCGEventOtherMouseUp;
			else eventType = kCGEventLeftMouseUp;
			break;
		case 2:
			if (button == 1) eventType = kCGEventRightMouseDragged;
			else if (button == 2) eventType = kCGEventOtherMouseDragged;
			else eventType = kCGEventLeftMouseDragged;
			break;
		default:
			return 2;
	}
	CGEventRef event = CGEventCreateMouseEvent(NULL, eventType, CGPointMake(x, y), (CGMouseButton)button);
	if (event == NULL) {
		return 1;
	}
	if (clickState > 0) {
		CGEventSetIntegerValueField(event, kCGMouseEventClickState, clickState);
	}
	CGEventPost(kCGHIDEventTap, event);
	CFRelease(event);
	return 0;
}

static int postScrollEvent(int deltaX, int deltaY) {
	CGEventRef event = CGEventCreateScrollWheelEvent(NULL, kCGScrollEventUnitLine, 2, deltaY, deltaX);
	if (event == NULL) {
		return 1;
	}
	CGEventPost(kCGHIDEventTap, event);
	CFRelease(event);
	return 0;
}

static int postKeyCodeEvent(int keyCode) {
	CGEventRef down = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)keyCode, true);
	if (down == NULL) {
		return 1;
	}
	CGEventPost(kCGHIDEventTap, down);
	CFRelease(down);
	CGEventRef up = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)keyCode, false);
	if (up == NULL) {
		return 1;
	}
	CGEventPost(kCGHIDEventTap, up);
	CFRelease(up);
	return 0;
}

static int postUnicodeTextEvent(const uint16_t* chars, long length) {
	CGEventRef down = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)0, true);
	if (down == NULL) {
		return 1;
	}
	CGEventKeyboardSetUnicodeString(down, (UniCharCount)length, (const UniChar*)chars);
	CGEventPost(kCGHIDEventTap, down);
	CFRelease(down);
	CGEventRef up = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)0, false);
	if (up == NULL) {
		return 1;
	}
	CGEventKeyboardSetUnicodeString(up, (UniCharCount)length, (const UniChar*)chars);
	CGEventPost(kCGHIDEventTap, up);
	CFRelease(up);
	return 0;
}
*/
import "C"

import (
	"fmt"
	"unicode/utf16"
	"unsafe"
)

func darwinPointerInputAvailability() (bool, string) {
	return true, ""
}

func darwinCurrentMousePosition() (int, int, error) {
	var x C.double
	var y C.double
	if code := int(C.currentMouseLocation(&x, &y)); code != 0 {
		return 0, 0, fmt.Errorf("读取当前鼠标位置失败")
	}
	return int(x), int(y), nil
}

func darwinPostMouseMove(x int, y int) error {
	if code := int(C.postMouseMove(C.double(x), C.double(y))); code != 0 {
		return fmt.Errorf("发布鼠标移动事件失败")
	}
	return nil
}

func darwinPostMouseButtonEvent(kind int, button int, x int, y int, clickState int) error {
	if code := int(C.postMouseButtonEvent(C.int(kind), C.int(button), C.double(x), C.double(y), C.long(clickState))); code != 0 {
		return fmt.Errorf("发布鼠标按键事件失败")
	}
	return nil
}

func darwinPostScrollEvent(deltaX int, deltaY int) error {
	if code := int(C.postScrollEvent(C.int(deltaX), C.int(deltaY))); code != 0 {
		return fmt.Errorf("发布滚轮事件失败")
	}
	return nil
}

func darwinUnicodeTextInputAvailable() bool {
	return true
}

func darwinPostKeyCode(keyCode int) error {
	if code := int(C.postKeyCodeEvent(C.int(keyCode))); code != 0 {
		return fmt.Errorf("发布按键事件失败")
	}
	return nil
}

func darwinPostUnicodeText(text string) error {
	units := utf16.Encode([]rune(text))
	if len(units) == 0 {
		return nil
	}
	if code := int(C.postUnicodeTextEvent((*C.uint16_t)(unsafe.Pointer(&units[0])), C.long(len(units)))); code != 0 {
		return fmt.Errorf("发布 Unicode 文本事件失败")
	}
	return nil
}