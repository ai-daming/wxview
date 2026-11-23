//go:build windows

package key

import (
	"bytes"
	"context"
	"errors"
	"os"
	"runtime"
	"testing"
	"time"
	"unsafe"
)

// Reads this test process's invented bytes, never a WeChat process. This checks
// the actual native adapter when tests run on Windows; cross-compiling it alone
// is not reported as a runtime pass.
func TestWindowsNativeMemoryAdapter(t *testing.T) {
	data := []byte("wxview synthetic native memory adapter fixture")
	address := uintptr(unsafe.Pointer(&data[0]))
	handle, _, err := procOpenProcess.Call(processVMRead|processQueryInformation, 0, uintptr(os.Getpid()))
	if handle == 0 {
		t.Fatalf("open test process: %v", err)
	}
	defer closeWindowsHandle(handle)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	regions, err := windowsMemoryRegions(ctx, handle)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, region := range regions {
		if uint64(address) >= region.base && uint64(address)-region.base < region.size {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("readable test buffer not found in native regions")
	}
	got, err := readWindowsProcessMemory(handle, address, len(data))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("native memory adapter did not return test buffer")
	}
	cancel()
	if _, err := windowsMemoryRegions(ctx, handle); !errors.Is(err, context.Canceled) {
		t.Fatal("native enumeration ignored cancellation")
	}
	runtime.KeepAlive(data)
}

func TestWindowsNativeMemoryEnumerationRejectsInvalidHandle(t *testing.T) {
	if _, err := windowsMemoryRegions(context.Background(), 0); err == nil {
		t.Fatal("invalid process handle was misreported as a complete empty scan")
	}
}
