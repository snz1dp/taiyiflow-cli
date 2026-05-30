//go:build darwin && cgo

package computerbridge

/*
#cgo LDFLAGS: -framework ApplicationServices
#include <ApplicationServices/ApplicationServices.h>
#include <stdint.h>
#include <stdlib.h>

typedef struct {
	uint32_t id;
	double x;
	double y;
	double width;
	double height;
	double pixelWidth;
	double pixelHeight;
	int primary;
} TaiyiDisplayLayout;

static int copyDisplayLayouts(TaiyiDisplayLayout* items, uint32_t capacity, uint32_t* count) {
	uint32_t displayCount = 0;
	CGError err = CGGetOnlineDisplayList(0, NULL, &displayCount);
	if (err != kCGErrorSuccess) {
		return (int)err;
	}
	if (count != NULL) {
		*count = displayCount;
	}
	if (displayCount == 0 || items == NULL || capacity == 0) {
		return 0;
	}
	CGDirectDisplayID* displays = (CGDirectDisplayID*)malloc(sizeof(CGDirectDisplayID) * displayCount);
	if (displays == NULL) {
		return -1;
	}
	err = CGGetOnlineDisplayList(displayCount, displays, &displayCount);
	if (err != kCGErrorSuccess) {
		free(displays);
		return (int)err;
	}
	uint32_t limit = displayCount;
	if (capacity < limit) {
		limit = capacity;
	}
	for (uint32_t index = 0; index < limit; index++) {
		CGRect bounds = CGDisplayBounds(displays[index]);
		CGDisplayModeRef mode = CGDisplayCopyDisplayMode(displays[index]);
		double logicalWidth = bounds.size.width;
		double logicalHeight = bounds.size.height;
		double pixelWidth = bounds.size.width;
		double pixelHeight = bounds.size.height;
		if (mode != NULL) {
			logicalWidth = (double)CGDisplayModeGetWidth(mode);
			logicalHeight = (double)CGDisplayModeGetHeight(mode);
			pixelWidth = (double)CGDisplayModeGetPixelWidth(mode);
			pixelHeight = (double)CGDisplayModeGetPixelHeight(mode);
			CGDisplayModeRelease(mode);
		}
		items[index].id = (uint32_t)displays[index];
		items[index].x = bounds.origin.x;
		items[index].y = bounds.origin.y;
		items[index].width = logicalWidth;
		items[index].height = logicalHeight;
		items[index].pixelWidth = pixelWidth;
		items[index].pixelHeight = pixelHeight;
		items[index].primary = CGDisplayIsMain(displays[index]) ? 1 : 0;
	}
	free(displays);
	return 0;
}
*/
import "C"

import (
	"fmt"
	"strconv"
	"unsafe"
)

func platformDisplayLayouts() ([]displayLayout, error) {
	var count C.uint32_t
	if code := int(C.copyDisplayLayouts(nil, 0, &count)); code != 0 {
		if code == -1 {
			return nil, fmt.Errorf("读取显示器布局失败: 内存分配失败")
		}
		return nil, fmt.Errorf("读取显示器布局失败")
	}
	if count == 0 {
		return nil, nil
	}
	items := make([]C.TaiyiDisplayLayout, int(count))
	if code := int(C.copyDisplayLayouts((*C.TaiyiDisplayLayout)(unsafe.Pointer(&items[0])), count, &count)); code != 0 {
		if code == -1 {
			return nil, fmt.Errorf("读取显示器布局失败: 内存分配失败")
		}
		return nil, fmt.Errorf("读取显示器布局失败")
	}
	layouts := make([]displayLayout, 0, int(count))
	for index := 0; index < int(count); index++ {
		item := items[index]
		logicalWidth := int(item.width)
		logicalHeight := int(item.height)
		pixelWidth := int(item.pixelWidth)
		pixelHeight := int(item.pixelHeight)
		scaleFactor := 1.0
		if logicalWidth > 0 && pixelWidth > 0 {
			scaleFactor = float64(pixelWidth) / float64(logicalWidth)
		} else if logicalHeight > 0 && pixelHeight > 0 {
			scaleFactor = float64(pixelHeight) / float64(logicalHeight)
		}
		layouts = append(layouts, displayLayout{
			DisplayID:    strconv.FormatUint(uint64(item.id), 10),
			DisplayIndex: index + 1,
			OriginX:      int(item.x),
			OriginY:      int(item.y),
			Width:        logicalWidth,
			Height:       logicalHeight,
			PixelWidth:   pixelWidth,
			PixelHeight:  pixelHeight,
			ScaleFactor:  scaleFactor,
			Primary:      int(item.primary) != 0,
		})
	}
	return layouts, nil
}
