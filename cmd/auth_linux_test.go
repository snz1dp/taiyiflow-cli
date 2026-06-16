//go:build linux

package cmd

import (
	"os"
	"testing"
)

func TestDefaultReadPlatformUUIDReadsDMIProductUUID(t *testing.T) {
	originalReadFile := deviceIDReadFile
	t.Cleanup(func() { deviceIDReadFile = originalReadFile })

	deviceIDReadFile = func(path string) ([]byte, error) {
		if path == "/sys/class/dmi/id/product_uuid" {
			return []byte("4C4C4544-0043-4B10-8031-B2C04F325A31\n"), nil
		}
		return nil, os.ErrNotExist
	}

	got := defaultReadPlatformUUID()
	if got != "4C4C4544-0043-4B10-8031-B2C04F325A31" {
		t.Fatalf("expected DMI product UUID, got %q", got)
	}
}

func TestDefaultReadPlatformUUIDReturnsEmptyOnNotSettable(t *testing.T) {
	originalReadFile := deviceIDReadFile
	t.Cleanup(func() { deviceIDReadFile = originalReadFile })

	deviceIDReadFile = func(path string) ([]byte, error) {
		if path == "/sys/class/dmi/id/product_uuid" {
			return []byte("Not Settable\n"), nil
		}
		return nil, os.ErrNotExist
	}

	got := defaultReadPlatformUUID()
	if got != "" {
		t.Fatalf("expected empty for 'Not Settable', got %q", got)
	}
}

func TestDefaultReadPlatformUUIDReturnsEmptyOnNotPresent(t *testing.T) {
	originalReadFile := deviceIDReadFile
	t.Cleanup(func() { deviceIDReadFile = originalReadFile })

	deviceIDReadFile = func(path string) ([]byte, error) {
		if path == "/sys/class/dmi/id/product_uuid" {
			return []byte("Not Present\n"), nil
		}
		return nil, os.ErrNotExist
	}

	got := defaultReadPlatformUUID()
	if got != "" {
		t.Fatalf("expected empty for 'Not Present', got %q", got)
	}
}

func TestDefaultReadPlatformUUIDReturnsEmptyOnPermissionDenied(t *testing.T) {
	originalReadFile := deviceIDReadFile
	t.Cleanup(func() { deviceIDReadFile = originalReadFile })

	deviceIDReadFile = func(_ string) ([]byte, error) {
		return nil, os.ErrPermission
	}

	got := defaultReadPlatformUUID()
	if got != "" {
		t.Fatalf("expected empty on permission denied, got %q", got)
	}
}
