//go:build windows

package key

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

const (
	processVMRead           = 0x0010
	processQueryInformation = 0x0400
	memCommit               = 0x00001000
	pageNoAccess            = 0x01
	pageReadOnly            = 0x02
	pageReadWrite           = 0x04
	pageWriteCopy           = 0x08
	pageExecuteRead         = 0x20
	pageExecuteReadWrite    = 0x40
	pageExecuteWriteCopy    = 0x80
	pageGuard               = 0x100
	errorInvalidParameter   = syscall.Errno(87) // Win32 ERROR_INVALID_PARAMETER
)

var (
	procOpenProcess       = kernel32DLL.NewProc("OpenProcess")
	procVirtualQueryEx    = kernel32DLL.NewProc("VirtualQueryEx")
	procReadProcessMemory = kernel32DLL.NewProc("ReadProcessMemory")
)

type memoryBasicInformation struct {
	BaseAddress       uintptr
	AllocationBase    uintptr
	AllocationProtect uint32
	PartitionID       uint16
	_                 uint16
	RegionSize        uintptr
	State             uint32
	Protect           uint32
	Type              uint32
}

func scanSQLCipherPragmaKey(ctx context.Context, pid int, saltHex string, page1 []byte) (string, error) {
	session, ok := ctx.Value(windowsScanSessionContextKey{}).(*windowsScanSession)
	if !ok {
		var cancel context.CancelFunc
		ctx, cancel = beginKeyScan(ctx)
		defer cancel()
		session = ctx.Value(windowsScanSessionContextKey{}).(*windowsScanSession)
	}
	return session.key(ctx, pid, saltHex, page1, func(ctx context.Context, budget *memoryReadBudget) (windowsKeyMaterial, error) {
		if err := ctx.Err(); err != nil {
			return windowsKeyMaterial{}, err
		}
		handle, _, err := procOpenProcess.Call(processVMRead|processQueryInformation, 0, uintptr(pid))
		if handle == 0 {
			return windowsKeyMaterial{}, fmt.Errorf("OpenProcess failed for pid %d: %w", pid, err)
		}
		defer closeWindowsHandle(handle)
		regions, err := windowsMemoryRegions(ctx, handle)
		if err != nil {
			return windowsKeyMaterial{}, err
		}
		read := func(address uint64, size int) ([]byte, error) {
			return readWindowsProcessMemory(handle, uintptr(address), size)
		}
		return collectWindowsKeyMaterial(ctx, regions, read, budget)
	})
}

func windowsMemoryRegions(ctx context.Context, handle uintptr) ([]memoryRegion, error) {
	return enumerateMemoryRegions(ctx, func(address uint64) (memoryRegionInfo, error) {
		var info memoryBasicInformation
		ok, _, err := procVirtualQueryEx.Call(handle, uintptr(address), uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
		if ok == 0 {
			// Per VirtualQueryEx's contract, querying beyond the process's
			// highest accessible address fails with ERROR_INVALID_PARAMETER.
			// Access/handle failures must not become a successful empty scan.
			if address > 0 && errors.Is(err, errorInvalidParameter) {
				return memoryRegionInfo{}, errMemoryRegionEnd
			}
			return memoryRegionInfo{}, fmt.Errorf("VirtualQueryEx failed: %w", err)
		}
		return memoryRegionInfo{
			memoryRegion: memoryRegion{uint64(info.BaseAddress), uint64(info.RegionSize)},
			readable:     info.State == memCommit && readableProtect(info.Protect),
		}, nil
	})
}

func readableProtect(protect uint32) bool {
	if protect&pageGuard != 0 || protect&pageNoAccess != 0 {
		return false
	}
	switch protect & 0xff {
	case pageReadOnly, pageReadWrite, pageWriteCopy, pageExecuteRead, pageExecuteReadWrite, pageExecuteWriteCopy:
		return true
	default:
		return false
	}
}

func readWindowsProcessMemory(handle uintptr, address uintptr, size int) ([]byte, error) {
	data := make([]byte, size)
	var n uintptr
	ok, _, err := procReadProcessMemory.Call(handle, address, uintptr(unsafe.Pointer(&data[0])), uintptr(size), uintptr(unsafe.Pointer(&n)))
	if n > uintptr(size) {
		return nil, errMemoryRead
	}
	if ok == 0 {
		return data[:n], err
	}
	return data[:n], nil
}
