package key

// Config.Cipher object layout and the known XOR mask are adapted from
// TANGandXUE/wcdb-key-tool, MIT, commit 79f1b5b92e12c66aa281b4a60a3c478b5f547dfa.
// Copyright (c) 2025 CloudDreamAI / TANGandXUE.
// See docs/third-party/wcdb-key-tool.LICENSE and docs/windows-key-scan.md.

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"strings"

	"wxview/internal/decrypt"
)

const (
	windowsConfigName     = "com.Tencent.WCDB.Config.Cipher"
	memoryChunkSize       = 2 * 1024 * 1024
	maxSQLCipherHexLen    = 192
	maxKeyMaterials       = 4096
	maxConfigBlob         = 1024
	maxWindowsUserAddress = uint64(1) << 47
)

var (
	errKeyVerification = errors.New("key candidates failed page 1 HMAC verification")
	errMemoryBudget    = errors.New("process memory scan byte budget exhausted")
	errMemoryRead      = errors.New("process memory scan had unreadable ranges")
	errCandidateBudget = errors.New("process memory scan candidate budget exhausted")
	errMemoryRegionEnd = errors.New("end of process address space")
	windowsConfigMask  = []byte{
		0xd2, 0xc7, 0x44, 0x24, 0x58, 0x02, 0, 0, 0, 0x48, 0x89, 0x44, 0x24, 0x50, 0x48, 0x8b,
		0x45, 0, 0x48, 0x84, 0x4c, 0x24, 0x48, 0x48, 0x89, 0x44, 0x25, 0x40, 0x48, 0x58, 0x4c, 0x24,
	}
)

// The traversal/decoding has no OS dependencies so the real algorithm can be
// tested on synthetic memory. Only enumeration and reads use Windows APIs.
type memoryRegion struct{ base, size uint64 }
type memoryRegionInfo struct {
	memoryRegion
	readable bool
}

func enumerateMemoryRegions(ctx context.Context, query func(uint64) (memoryRegionInfo, error)) ([]memoryRegion, error) {
	var regions []memoryRegion
	for address := uint64(0); address < maxWindowsUserAddress; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := query(address)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			if errors.Is(err, errMemoryRegionEnd) {
				break
			}
			return nil, err
		}
		if info.size > ^uint64(0)-info.base || info.base > address || address-info.base >= info.size {
			return nil, errMemoryRead
		}
		if info.readable {
			regions = append(regions, info.memoryRegion)
		}
		address = info.base + info.size
	}
	return regions, nil
}

type memoryReader func(uint64, int) ([]byte, error)
type memoryReadBudget struct{ remaining uint64 }
type windowsKeyMaterial struct {
	literals map[string]struct{}
	blobs    [][]byte
}

func (b *memoryReadBudget) read(ctx context.Context, read memoryReader, address uint64, size int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if size <= 0 || uint64(size) > b.remaining {
		return nil, errMemoryBudget
	}
	b.remaining -= uint64(size)
	data, err := read(address, size)
	if len(data) > size {
		return nil, errMemoryRead
	}
	return data, err
}

// Partial reads can be inspected, but never joined across an unreadable gap.
// Failed ranges are skipped; fatal cancellation/budget errors stop the scan.
func walkMemory(ctx context.Context, regions []memoryRegion, read memoryReader, budget *memoryReadBudget, chunkSize, overlap int, visit func(uint64, []byte) error) error {
	var incomplete bool
	var tail []byte
	var previousEnd uint64
	for _, region := range regions {
		if region.size > ^uint64(0)-region.base {
			return errMemoryRead
		}
		if region.base != previousEnd {
			tail = nil
		}
		previousEnd = region.base + region.size
		for offset := uint64(0); offset < region.size; {
			size := min(uint64(chunkSize), region.size-offset)
			data, err := budget.read(ctx, read, region.base+offset, int(size))
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errMemoryBudget) {
				return err
			}
			base := region.base + offset
			if len(data) > 0 {
				if len(tail) > 0 {
					base -= uint64(len(tail))
					data = append(tail, data...)
				}
				if err := visit(base, data); err != nil {
					return err
				}
			}
			// data may now include tail; the address span determines whether
			// the requested chunk was fully read, independent of overlap.
			complete := err == nil && base+uint64(len(data)) == region.base+offset+size
			if complete && overlap > 0 {
				tail = append([]byte(nil), data[max(0, len(data)-overlap):]...)
			} else {
				tail = nil
			}
			if !complete {
				incomplete = true
			}
			offset += size
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if incomplete {
		return errMemoryRead
	}
	return nil
}

func visitSQLCipherLiterals(data []byte, visit func(string) error) error {
	for i := 0; i+2 < len(data); i++ {
		if (data[i] != 'x' && data[i] != 'X') || data[i+1] != '\'' {
			continue
		}
		start, end := i+2, i+2
		for end < len(data) && end-start < maxSQLCipherHexLen && isHexByte(data[end]) {
			end++
		}
		if end-start >= 64 && (end-start)%2 == 0 && end < len(data) && data[end] == '\'' {
			if err := visit(strings.ToLower(string(data[start:end]))); err != nil {
				return err
			}
			i = end
		}
	}
	return nil
}

func collectWindowsKeyMaterial(ctx context.Context, regions []memoryRegion, read memoryReader, budget *memoryReadBudget) (windowsKeyMaterial, error) {
	material := windowsKeyMaterial{literals: make(map[string]struct{})}
	names := make(map[uint64]struct{})
	err := walkMemory(ctx, regions, read, budget, memoryChunkSize, maxSQLCipherHexLen+2, func(base uint64, data []byte) error {
		if err := visitSQLCipherLiterals(data, func(s string) error {
			material.literals[s] = struct{}{}
			if len(material.literals) > maxKeyMaterials {
				return errCandidateBudget
			}
			return nil
		}); err != nil {
			return err
		}
		for offset := 0; offset < len(data); {
			pos := bytes.Index(data[offset:], []byte(windowsConfigName))
			if pos < 0 {
				break
			}
			names[base+uint64(offset+pos)] = struct{}{}
			if len(names) > maxKeyMaterials {
				return errCandidateBudget
			}
			offset += pos + 1
		}
		return nil
	})
	if err != nil && !errors.Is(err, errMemoryRead) {
		return material, err
	}
	if len(names) == 0 {
		return material, err
	}
	firstErr := err
	seen := make(map[uint64]struct{})
	readObject := func(addr uint64, size int) ([]byte, error) {
		if addr < 0x10000 || addr >= maxWindowsUserAddress || uint64(size) > maxWindowsUserAddress-addr {
			return nil, errMemoryRead
		}
		data, err := budget.read(ctx, read, addr, size)
		if err == nil && len(data) != size {
			err = errMemoryRead
		}
		return data, err
	}
	err = walkMemory(ctx, regions, read, budget, memoryChunkSize, 0x80, func(base uint64, data []byte) error {
		for pos := 0; pos+16 <= len(data); pos++ {
			// Check the short length first to avoid a map lookup per byte.
			if data[pos+8] != byte(len(windowsConfigName)) || binary.LittleEndian.Uint64(data[pos+8:]) != uint64(len(windowsConfigName)) {
				continue
			}
			if _, ok := names[binary.LittleEndian.Uint64(data[pos:])]; !ok {
				continue
			}
			if base+uint64(pos) < 0x10010 {
				continue
			}
			node, err := readObject(base+uint64(pos)-0x10, 0x50)
			if err == nil {
				if _, ok := names[binary.LittleEndian.Uint64(node[0x10:])]; !ok || binary.LittleEndian.Uint64(node[0x18:]) != uint64(len(windowsConfigName)) {
					continue
				}
				pointer := binary.LittleEndian.Uint64(node[0x28:])
				if _, ok := seen[pointer]; ok {
					continue
				}
				seen[pointer] = struct{}{}
				if len(seen) > maxKeyMaterials {
					return errCandidateBudget
				}
				if pointer < 0x10000 || pointer >= maxWindowsUserAddress-0xb0 {
					continue
				}
				object, readErr := readObject(pointer+0x88, 0x28)
				err = readErr
				if err == nil {
					length := binary.LittleEndian.Uint64(object[0x10:])
					if length == 0 || length > maxConfigBlob {
						continue
					}
					blob, readErr := readObject(binary.LittleEndian.Uint64(object[8:]), int(length))
					err = readErr
					if err == nil {
						material.blobs = append(material.blobs, append([]byte(nil), blob...))
					}
				}
			}
			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errMemoryBudget) {
					return err
				}
				firstErr = errMemoryRead
			}
		}
		return nil
	})
	if err != nil {
		return material, err
	}
	return material, firstErr
}

func (material windowsKeyMaterial) key(ctx context.Context, salt string, page []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	salt = strings.ToLower(salt)
	seen := make(map[string]struct{})
	var result string
	try := func(key string) {
		if result != "" {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		if decrypt.ValidateRawHexKey(page, key) {
			result = key
		}
	}
	for _, blob := range material.blobs {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		masks := [][]byte{windowsConfigMask}
		if len(blob) == 99 && len(salt) == 32 {
			// x'<key64><salt32>': bytes [66,98) give one complete
			// period of a repeating 32-byte mask. HMAC remains decisive.
			mask := make([]byte, 32)
			for i := range salt {
				mask[(66+i)%32] = blob[66+i] ^ salt[i]
			}
			masks = append(masks, mask)
		}
		for _, mask := range masks {
			decoded := make([]byte, len(blob))
			for i := range blob {
				decoded[i] = blob[i] ^ mask[i%len(mask)]
			}
			_ = visitSQLCipherLiterals(decoded, func(run string) error {
				try(run[:64])
				if len(run) > 96 {
					for start := 32; start+64 <= len(run); start += 32 {
						try(run[start : start+64])
					}
					try(run[len(run)-64:])
				}
				return nil
			})
		}
		if result != "" {
			return result, nil
		}
	}
	for run := range material.literals {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if len(run) == 64 || (len(run) >= 96 && run[len(run)-32:] == salt) {
			try(run[:64])
		}
		if result != "" {
			return result, nil
		}
	}
	if len(seen) > 0 {
		return "", errKeyVerification
	}
	return "", nil
}

func isHexByte(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}
