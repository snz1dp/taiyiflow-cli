//go:build windows

package computerbridge

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32DLL               = windows.NewLazySystemDLL("user32.dll")
	procEnumDisplayMonitors = user32DLL.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW     = user32DLL.NewProc("GetMonitorInfoW")
	procEnumDisplaySettings = user32DLL.NewProc("EnumDisplaySettingsW")
)

const (
	monitorInfoPrimaryFlag = 0x00000001
	enumCurrentSettings    = 0xFFFFFFFF
)

type winRect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

type winMonitorInfoEx struct {
	CbSize    uint32
	RcMonitor winRect
	RcWork    winRect
	DwFlags   uint32
	SzDevice  [32]uint16
}

type winDevMode struct {
	DeviceName       [32]uint16
	SpecVersion      uint16
	DriverVersion    uint16
	Size             uint16
	DriverExtra      uint16
	Fields           uint32
	Orientation      int16
	PaperSize        int16
	PaperLength      int16
	PaperWidth       int16
	Scale            int16
	Copies           int16
	DefaultSource    int16
	PrintQuality     int16
	Color            int16
	Duplex           int16
	YResolution      int16
	TTOption         int16
	Collate          int16
	FormName         [32]uint16
	LogPixels        uint16
	BitsPerPel       uint32
	PelsWidth        uint32
	PelsHeight       uint32
	DisplayFlags     uint32
	DisplayFrequency uint32
	ICMMethod        uint32
	ICMIntent        uint32
	MediaType        uint32
	DitherType       uint32
	Reserved1        uint32
	Reserved2        uint32
	PanningWidth     uint32
	PanningHeight    uint32
}

func platformDisplayLayouts() ([]displayLayout, error) {
	layouts := []displayLayout{}
	var enumErr error
	callback := syscall.NewCallback(func(hMonitor uintptr, _hdc uintptr, _lprcMonitor uintptr, _dwData uintptr) uintptr {
		info := winMonitorInfoEx{CbSize: uint32(unsafe.Sizeof(winMonitorInfoEx{}))}
		ret, _, callErr := procGetMonitorInfoW.Call(hMonitor, uintptr(unsafe.Pointer(&info)))
		if ret == 0 {
			if callErr != windows.ERROR_SUCCESS {
				enumErr = callErr
			} else {
				enumErr = fmt.Errorf("GetMonitorInfoW 返回失败")
			}
			return 0
		}
		layout := displayLayout{
			DisplayID: strings.TrimSpace(windows.UTF16ToString(info.SzDevice[:])),
			OriginX:   int(info.RcMonitor.Left),
			OriginY:   int(info.RcMonitor.Top),
			Width:     int(info.RcMonitor.Right - info.RcMonitor.Left),
			Height:    int(info.RcMonitor.Bottom - info.RcMonitor.Top),
			Primary:   info.DwFlags&monitorInfoPrimaryFlag != 0,
		}
		if pixelWidth, pixelHeight, modeErr := currentWindowsDisplayMode(layout.DisplayID); modeErr == nil {
			layout.PixelWidth = pixelWidth
			layout.PixelHeight = pixelHeight
		}
		layouts = append(layouts, layout)
		return 1
	})
	ret, _, callErr := procEnumDisplayMonitors.Call(0, 0, callback, 0)
	if ret == 0 {
		if enumErr != nil {
			return nil, fmt.Errorf("读取显示器布局失败: %w", enumErr)
		}
		if callErr != windows.ERROR_SUCCESS {
			return nil, fmt.Errorf("读取显示器布局失败: %w", callErr)
		}
		return nil, fmt.Errorf("读取显示器布局失败")
	}
	if enumErr != nil {
		return nil, fmt.Errorf("读取显示器布局失败: %w", enumErr)
	}
	if len(layouts) == 0 {
		return nil, fmt.Errorf("未发现在线显示器")
	}
	sort.SliceStable(layouts, func(left int, right int) bool {
		if layouts[left].Primary != layouts[right].Primary {
			return layouts[left].Primary
		}
		if layouts[left].OriginX != layouts[right].OriginX {
			return layouts[left].OriginX < layouts[right].OriginX
		}
		if layouts[left].OriginY != layouts[right].OriginY {
			return layouts[left].OriginY < layouts[right].OriginY
		}
		return layouts[left].DisplayID < layouts[right].DisplayID
	})
	for index := range layouts {
		layouts[index].DisplayIndex = index + 1
		if strings.TrimSpace(layouts[index].DisplayID) == "" {
			layouts[index].DisplayID = strconv.Itoa(index + 1)
		}
	}
	return layouts, nil
}

func currentWindowsDisplayMode(deviceName string) (int, int, error) {
	trimmed := strings.TrimSpace(deviceName)
	if trimmed == "" {
		return 0, 0, fmt.Errorf("缺少显示设备名")
	}
	devicePtr, err := windows.UTF16PtrFromString(trimmed)
	if err != nil {
		return 0, 0, err
	}
	mode := winDevMode{Size: uint16(unsafe.Sizeof(winDevMode{}))}
	ret, _, _ := procEnumDisplaySettings.Call(
		uintptr(unsafe.Pointer(devicePtr)),
		uintptr(enumCurrentSettings),
		uintptr(unsafe.Pointer(&mode)),
	)
	if ret == 0 {
		return 0, 0, fmt.Errorf("EnumDisplaySettingsW 返回失败")
	}
	return int(mode.PelsWidth), int(mode.PelsHeight), nil
}
