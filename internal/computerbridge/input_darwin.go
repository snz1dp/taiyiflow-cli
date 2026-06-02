//go:build darwin && cgo

package computerbridge

/*
#cgo LDFLAGS: -framework ApplicationServices -framework AppKit -framework Foundation
#include <ApplicationServices/ApplicationServices.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>

int clipboardSnapshotImpl(void** outBytes, long* outLen, void** outErrBytes, long* outErrLen);
int clipboardRestoreImpl(const void* bytes, long length, void** outErrBytes, long* outErrLen);
int clipboardSetTextImpl(const uint16_t* chars, long length, void** outErrBytes, long* outErrLen);

static int clipboardSnapshot(void** outBytes, long* outLen, void** outErrBytes, long* outErrLen) {
	return clipboardSnapshotImpl(outBytes, outLen, outErrBytes, outErrLen);
}

static int clipboardRestore(const void* bytes, long length, void** outErrBytes, long* outErrLen) {
	return clipboardRestoreImpl(bytes, length, outErrBytes, outErrLen);
}

static int clipboardSetText(const uint16_t* chars, long length, void** outErrBytes, long* outErrLen) {
	return clipboardSetTextImpl(chars, length, outErrBytes, outErrLen);
}

static void freeMemory(void* ptr) {
	if (ptr != NULL) {
		free(ptr);
	}
	return;
}

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

static int postKeyboardEventWithFlags(int keyCode, bool down, CGEventFlags flags) {
	CGEventRef event = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)keyCode, down);
	if (event == NULL) {
		return 1;
	}
	CGEventSetFlags(event, flags);
	CGEventPost(kCGHIDEventTap, event);
	CFRelease(event);
	return 0;
}

static int postModifiedKeyCodeEvent(int keyCode, int command, int shift, int option, int control) {
	CGEventFlags flags = 0;
	if (control) {
		flags |= kCGEventFlagMaskControl;
		if (postKeyboardEventWithFlags(59, true, flags) != 0) return 1;
	}
	if (option) {
		flags |= kCGEventFlagMaskAlternate;
		if (postKeyboardEventWithFlags(58, true, flags) != 0) return 1;
	}
	if (shift) {
		flags |= kCGEventFlagMaskShift;
		if (postKeyboardEventWithFlags(56, true, flags) != 0) return 1;
	}
	if (command) {
		flags |= kCGEventFlagMaskCommand;
		if (postKeyboardEventWithFlags(55, true, flags) != 0) return 1;
	}
	if (postKeyboardEventWithFlags(keyCode, true, flags) != 0) return 1;
	if (postKeyboardEventWithFlags(keyCode, false, flags) != 0) return 1;
	if (command) {
		flags &= ~kCGEventFlagMaskCommand;
		if (postKeyboardEventWithFlags(55, false, flags) != 0) return 1;
	}
	if (shift) {
		flags &= ~kCGEventFlagMaskShift;
		if (postKeyboardEventWithFlags(56, false, flags) != 0) return 1;
	}
	if (option) {
		flags &= ~kCGEventFlagMaskAlternate;
		if (postKeyboardEventWithFlags(58, false, flags) != 0) return 1;
	}
	if (control) {
		flags &= ~kCGEventFlagMaskControl;
		if (postKeyboardEventWithFlags(59, false, flags) != 0) return 1;
	}
	return 0;
}

static int postCommandVShortcutEvent(void) {
	return postModifiedKeyCodeEvent(9, 1, 0, 0, 0);
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

func darwinPostModifiedKeyCode(keyCode int, command bool, shift bool, option bool, control bool) error {
	if code := int(C.postModifiedKeyCodeEvent(C.int(keyCode), boolToCInt(command), boolToCInt(shift), boolToCInt(option), boolToCInt(control))); code != 0 {
		return fmt.Errorf("发布带修饰键的按键事件失败")
	}
	return nil
}

func boolToCInt(value bool) C.int {
	if value {
		return 1
	}
	return 0
}

func darwinPostCommandVShortcut() error {
	if code := int(C.postCommandVShortcutEvent()); code != 0 {
		return fmt.Errorf("发布 Command+V 粘贴快捷键失败")
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

func darwinCaptureClipboard() ([]byte, error) {
	var dataPtr unsafe.Pointer
	var dataLen C.long
	var errPtr unsafe.Pointer
	var errLen C.long
	if code := int(C.clipboardSnapshot(&dataPtr, &dataLen, &errPtr, &errLen)); code != 0 {
		return nil, darwinClipboardError("读取系统剪贴板快照失败", errPtr, errLen)
	}
	defer C.freeMemory(dataPtr)
	if dataLen == 0 || dataPtr == nil {
		return nil, nil
	}
	return cloneForeignBytes(dataPtr, dataLen), nil
}

func darwinRestoreClipboard(snapshot []byte) error {
	var dataPtr unsafe.Pointer
	if len(snapshot) > 0 {
		dataPtr = unsafe.Pointer(&snapshot[0])
	}
	var errPtr unsafe.Pointer
	var errLen C.long
	if code := int(C.clipboardRestore(dataPtr, C.long(len(snapshot)), &errPtr, &errLen)); code != 0 {
		return darwinClipboardError("恢复系统剪贴板失败", errPtr, errLen)
	}
	return nil
}

func darwinSetClipboardText(text string) error {
	units := utf16.Encode([]rune(text))
	var unitPtr *C.uint16_t
	if len(units) > 0 {
		unitPtr = (*C.uint16_t)(unsafe.Pointer(&units[0]))
	}
	var errPtr unsafe.Pointer
	var errLen C.long
	if code := int(C.clipboardSetText(unitPtr, C.long(len(units)), &errPtr, &errLen)); code != 0 {
		return darwinClipboardError("写入系统剪贴板失败", errPtr, errLen)
	}
	return nil
}

func cloneForeignBytes(ptr unsafe.Pointer, length C.long) []byte {
	if ptr == nil || length <= 0 {
		return nil
	}
	view := unsafe.Slice((*byte)(ptr), int(length))
	cloned := make([]byte, len(view))
	copy(cloned, view)
	return cloned
}

func darwinClipboardError(prefix string, errPtr unsafe.Pointer, errLen C.long) error {
	if errPtr != nil {
		defer C.freeMemory(errPtr)
		message := string(cloneForeignBytes(errPtr, errLen))
		if message != "" {
			return fmt.Errorf("%s: %s", prefix, message)
		}
	}
	return fmt.Errorf("%s", prefix)
}
