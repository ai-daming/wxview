//go:build darwin && cgo

package key

/*
#include <ctype.h>
#include <mach/mach.h>
#include <mach/mach_vm.h>
#include <stdlib.h>
#include <string.h>

#define WXVIEW_HEX_PATTERN_LEN 96
#define WXVIEW_PATTERN_LEN (WXVIEW_HEX_PATTERN_LEN + 3)
#define WXVIEW_SCAN_CHUNK_SIZE (2 * 1024 * 1024)

static mach_port_t wxview_mach_task_self(void) {
	return mach_task_self();
}

static int wxview_is_hex_char(unsigned char c) {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F');
}

static void wxview_lower_hex(char *s) {
	for (int i = 0; s[i] != '\0'; i++) {
		s[i] = (char)tolower((unsigned char)s[i]);
	}
}

static int wxview_scan_sqlcipher_buffer(
	unsigned char *buf,
	mach_msg_type_number_t len,
	const char *target_salt_hex,
	char *key_hex_out
) {
	if (len < WXVIEW_PATTERN_LEN) return 0;

	for (mach_msg_type_number_t i = 0; i + WXVIEW_PATTERN_LEN <= len; i++) {
		if (buf[i] != 'x' || buf[i + 1] != '\'') continue;

		int valid = 1;
		for (int j = 0; j < WXVIEW_HEX_PATTERN_LEN; j++) {
			if (!wxview_is_hex_char(buf[i + 2 + j])) {
				valid = 0;
				break;
			}
		}
		if (!valid) continue;
		if (buf[i + 2 + WXVIEW_HEX_PATTERN_LEN] != '\'') continue;

		char salt_hex[33];
		memcpy(salt_hex, buf + i + 2 + 64, 32);
		salt_hex[32] = '\0';
		wxview_lower_hex(salt_hex);
		if (strcmp(salt_hex, target_salt_hex) != 0) continue;

		memcpy(key_hex_out, buf + i + 2, 64);
		key_hex_out[64] = '\0';
		wxview_lower_hex(key_hex_out);
		return 1;
	}

	return 0;
}

static int wxview_scan_sqlcipher_key_for_salt(pid_t pid, const char *target_salt_hex, char *key_hex_out) {
	mach_port_t task = MACH_PORT_NULL;
	kern_return_t kr = task_for_pid(wxview_mach_task_self(), pid, &task);
	if (kr != KERN_SUCCESS) return (int)kr;

	mach_vm_address_t addr = 0;
	while (1) {
		mach_vm_size_t size = 0;
		vm_region_basic_info_data_64_t info;
		mach_msg_type_number_t info_count = VM_REGION_BASIC_INFO_COUNT_64;
		mach_port_t object_name = MACH_PORT_NULL;

		kr = mach_vm_region(
			task,
			&addr,
			&size,
			VM_REGION_BASIC_INFO_64,
			(vm_region_info_t)&info,
			&info_count,
			&object_name
		);
		if (kr != KERN_SUCCESS) break;
		if (size == 0) {
			addr++;
			continue;
		}

		if ((info.protection & (VM_PROT_READ | VM_PROT_WRITE)) == (VM_PROT_READ | VM_PROT_WRITE)) {
			mach_vm_address_t current = addr;
			mach_vm_address_t end = addr + size;
			while (current < end) {
				mach_vm_size_t chunk_size = end - current;
				if (chunk_size > WXVIEW_SCAN_CHUNK_SIZE) chunk_size = WXVIEW_SCAN_CHUNK_SIZE;

				vm_offset_t data = 0;
				mach_msg_type_number_t data_count = 0;
				kr = mach_vm_read(task, current, chunk_size, &data, &data_count);
				if (kr == KERN_SUCCESS && data_count > 0) {
					int found = wxview_scan_sqlcipher_buffer((unsigned char *)data, data_count, target_salt_hex, key_hex_out);
					mach_vm_deallocate(wxview_mach_task_self(), data, data_count);
					if (found) return 0;
				}

				if (chunk_size > WXVIEW_PATTERN_LEN) {
					current += chunk_size - WXVIEW_PATTERN_LEN;
				} else {
					current += chunk_size;
				}
			}
		}

		mach_vm_address_t next = addr + size;
		if (next <= addr) break;
		addr = next;
	}

	return -1;
}
*/
import "C"

import (
	"context"
	"fmt"
	"unsafe"
)

func scanSQLCipherPragmaKey(_ context.Context, pid int, saltHex string, _ []byte) (string, error) {
	cSalt := C.CString(saltHex)
	defer C.free(unsafe.Pointer(cSalt))

	keyBuf := make([]C.char, 65)
	ret := C.wxview_scan_sqlcipher_key_for_salt(C.pid_t(pid), cSalt, &keyBuf[0])
	if ret == 0 {
		return C.GoString(&keyBuf[0]), nil
	}
	if ret == -1 {
		return "", nil
	}
	return "", fmt.Errorf("task_for_pid failed for pid %d: %d", pid, int(ret))
}
