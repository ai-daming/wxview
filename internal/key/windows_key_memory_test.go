package key

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These are artificial process bytes and authenticated page-1 fixtures, not
// process dumps or user keys. The reader is the only Windows API boundary.
func windowsMemoryFixture(t *testing.T, literal string, mask []byte) ([]memoryRegion, memoryReader, []byte) {
	t.Helper()
	data := make([]byte, 8192)
	copy(data[0x100:], "com.Tencent.WCDB.Config.Cipher")
	// Node + 0x10: name pointer/length; node + 0x28: Config.Cipher pointer.
	binary.LittleEndian.PutUint64(data[0x210:], 0x10100)
	binary.LittleEndian.PutUint64(data[0x218:], 30)
	binary.LittleEndian.PutUint64(data[0x228:], 0x10400)
	// Config + 0x90/0x98: encoded key blob pointer/length.
	binary.LittleEndian.PutUint64(data[0x490:], 0x10800)
	binary.LittleEndian.PutUint64(data[0x498:], uint64(len(literal)))
	for i := range literal {
		data[0x800+i] = literal[i] ^ mask[i%len(mask)]
	}
	reader := func(addr uint64, size int) ([]byte, error) {
		if addr < 0x10000 || addr > 0x12000 || uint64(size) > 0x12000-addr {
			return nil, errors.New("unreadable synthetic range")
		}
		return data[addr-0x10000 : addr-0x10000+uint64(size)], nil
	}
	return []memoryRegion{{0x10000, uint64(len(data))}}, reader, data
}

func memoryPage(t *testing.T) (capturedKey, []byte) {
	t.Helper()
	root := t.TempDir()
	candidate := captureFixture(t, root, "contact/contact.db", 7)
	page, err := os.ReadFile(filepath.Join(root, "contact/contact.db"))
	if err != nil {
		t.Fatal(err)
	}
	return candidate, page
}

func TestWindowsRuntimeKeyRequiresPageHMAC(t *testing.T) {
	candidate, page := memoryPage(t)
	// A different mask from the known AMD64 mask exercises derivation from
	// the known 32 ASCII salt bytes, which span a complete mask period.
	mask := bytes.Repeat([]byte{0xa5}, 32)
	for _, tc := range []struct {
		name, literal string
		valid         bool
	}{
		{"valid", "x'" + candidate.Key + candidate.Salt + "'", true},
		{"wrong key", "x'" + strings.Repeat("ff", 32) + candidate.Salt + "'", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			regions, read, _ := windowsMemoryFixture(t, tc.literal, mask)
			material, err := collectWindowsKeyMaterial(context.Background(), regions, read, &memoryReadBudget{remaining: 1 << 20})
			if err != nil {
				t.Fatal(err)
			}
			got, err := material.key(context.Background(), candidate.Salt, page)
			if tc.valid {
				if err != nil || got != candidate.Key {
					t.Fatal("runtime key was not authenticated")
				}
			} else if got != "" || !errors.Is(err, errKeyVerification) {
				t.Fatal("wrong runtime key was not classified as verification failure")
			}
		})
	}
}

func TestWindowsRuntimeKnownMaskAndLegacyFallback(t *testing.T) {
	candidate, page := memoryPage(t)
	mask := []byte{0xd2, 0xc7, 0x44, 0x24, 0x58, 0x02, 0, 0, 0, 0x48, 0x89, 0x44, 0x24, 0x50, 0x48, 0x8b,
		0x45, 0, 0x48, 0x84, 0x4c, 0x24, 0x48, 0x48, 0x89, 0x44, 0x25, 0x40, 0x48, 0x58, 0x4c, 0x24}
	for _, tc := range []struct{ name, blob, legacy string }{
		{"raw key runtime blob", "X'" + candidate.Key + "'", ""},
		{"legacy without object", "not a key", "x'" + candidate.Key + candidate.Salt + "'"},
		{"bad runtime falls back", "x'" + strings.Repeat("ff", 32) + candidate.Salt + "'", "x'" + candidate.Key + "'"},
		{"legacy extended literal", "", "x'" + candidate.Key + strings.Repeat("ab", 16) + candidate.Salt + "'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			regions, read, data := windowsMemoryFixture(t, tc.blob, mask)
			if tc.name == "legacy without object" {
				clear(data[0x100:0x120])
			}
			copy(data[0x1000:], tc.legacy)
			material, err := collectWindowsKeyMaterial(context.Background(), regions, read, &memoryReadBudget{remaining: 1 << 20})
			if err != nil {
				t.Fatal(err)
			}
			got, err := material.key(context.Background(), candidate.Salt, page)
			if err != nil || got != candidate.Key {
				t.Fatal("compatible key path failed")
			}
		})
	}
}

func TestWindowsRuntimeRejectsMissingAndMalformedObjects(t *testing.T) {
	candidate, page := memoryPage(t)
	for _, mutation := range []func([]byte){
		func(d []byte) { clear(d[0x100:0x120]) },
		func(d []byte) { binary.LittleEndian.PutUint64(d[0x218:], 29) },
		func(d []byte) { binary.LittleEndian.PutUint64(d[0x228:], ^uint64(0)) },
		func(d []byte) { binary.LittleEndian.PutUint64(d[0x490:], 0x20000) },
		func(d []byte) { binary.LittleEndian.PutUint64(d[0x498:], 1025) },
	} {
		regions, read, data := windowsMemoryFixture(t, "x'"+candidate.Key+candidate.Salt+"'", bytes.Repeat([]byte{0xa5}, 32))
		mutation(data)
		material, _ := collectWindowsKeyMaterial(context.Background(), regions, read, &memoryReadBudget{remaining: 1 << 20})
		got, _ := material.key(context.Background(), candidate.Salt, page)
		if got != "" {
			t.Fatal("malformed runtime object yielded a key")
		}
	}
}

func TestMemoryWalkDoesNotJoinUnreadableGaps(t *testing.T) {
	for _, short := range []bool{false, true} {
		var seen []string
		read := func(addr uint64, size int) ([]byte, error) {
			switch addr {
			case 0x10000:
				return []byte("ABCD"), nil
			case 0x10004:
				if short {
					return []byte("E"), nil
				}
				return nil, errors.New("unreadable")
			default:
				return []byte("IJKL"), nil
			}
		}
		_ = walkMemory(context.Background(), []memoryRegion{{0x10000, 12}}, read, &memoryReadBudget{remaining: 100}, 4, 3,
			func(_ uint64, data []byte) error { seen = append(seen, string(data)); return nil })
		if len(seen) < 2 || seen[len(seen)-1] != "IJKL" {
			t.Fatal("reader joined bytes across a missing memory span")
		}
	}
}

func TestMemoryWalkPreservesContiguousBoundary(t *testing.T) {
	data := []byte("0123needle45")
	var found bool
	read := func(addr uint64, n int) ([]byte, error) { return data[addr-0x10000 : addr-0x10000+uint64(n)], nil }
	err := walkMemory(context.Background(), []memoryRegion{{0x10000, uint64(len(data))}}, read, &memoryReadBudget{remaining: 100}, 7, 5,
		func(base uint64, chunk []byte) error {
			if p := bytes.Index(chunk, []byte("needle")); p >= 0 {
				found = base+uint64(p) == 0x10004
			}
			return nil
		})
	if err != nil || !found {
		t.Fatal("contiguous cross-chunk pattern was lost or addressed incorrectly")
	}
}

func TestWindowsMemoryCancellationAndBudget(t *testing.T) {
	candidate, page := memoryPage(t)
	regions, read, _ := windowsMemoryFixture(t, "x'"+candidate.Key+candidate.Salt+"'", bytes.Repeat([]byte{0xa5}, 32))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := collectWindowsKeyMaterial(ctx, regions, read, &memoryReadBudget{remaining: 1 << 20}); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled scan continued")
	}
	if _, err := collectWindowsKeyMaterial(context.Background(), regions, read, &memoryReadBudget{remaining: 1}); !errors.Is(err, errMemoryBudget) {
		t.Fatal("scan exceeded its byte budget")
	}
	material, err := collectWindowsKeyMaterial(context.Background(), regions, read, &memoryReadBudget{remaining: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := material.key(ctx, candidate.Salt, page); got != "" || !errors.Is(err, context.Canceled) {
		t.Fatal("cached key bypassed cancellation")
	}
	_, err = collectWindowsKeyMaterial(context.Background(), regions, func(uint64, int) ([]byte, error) { return nil, errors.New("access denied") }, &memoryReadBudget{remaining: 1 << 20})
	if !errors.Is(err, errMemoryRead) {
		t.Fatal("unreadable process was not distinguished from no candidate")
	}
}

func TestWindowsRuntimeObjectAcrossMemoryChunks(t *testing.T) {
	candidate, page := memoryPage(t)
	names := make([]byte, memoryChunkSize+64)
	objects := make([]byte, memoryChunkSize+512)
	nameAddress := uint64(0x10000 + memoryChunkSize - 12)
	copy(names[memoryChunkSize-12:], "com.Tencent.WCDB.Config.Cipher")
	// The pointer/length reference also straddles a different chunk boundary.
	pos := memoryChunkSize - 8
	binary.LittleEndian.PutUint64(objects[pos:], nameAddress)
	binary.LittleEndian.PutUint64(objects[pos+8:], 30)
	binary.LittleEndian.PutUint64(objects[pos+0x18:], 0x400400)
	binary.LittleEndian.PutUint64(objects[0x490:], 0x400800)
	literal := "x'" + candidate.Key + candidate.Salt + "'"
	binary.LittleEndian.PutUint64(objects[0x498:], uint64(len(literal)))
	for i := range literal {
		objects[0x800+i] = literal[i] ^ 0xa5
	}
	regions := []memoryRegion{{0x10000, uint64(len(names))}, {0x400000, uint64(len(objects))}}
	read := func(addr uint64, size int) ([]byte, error) {
		for i, region := range regions {
			if addr >= region.base && addr-region.base <= region.size && uint64(size) <= region.size-(addr-region.base) {
				data := names
				if i == 1 {
					data = objects
				}
				return data[addr-region.base : addr-region.base+uint64(size)], nil
			}
		}
		return nil, errors.New("unmapped synthetic memory")
	}
	material, err := collectWindowsKeyMaterial(context.Background(), regions, read, &memoryReadBudget{remaining: 12 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if key, err := material.key(context.Background(), candidate.Salt, page); err != nil || key != candidate.Key {
		t.Fatal("cross-chunk object graph was not authenticated")
	}
}

func TestWindowsKeyDiagnosticsDoNotExposeMaterial(t *testing.T) {
	candidate, page := memoryPage(t)
	regions, read, _ := windowsMemoryFixture(t, "x'"+candidate.Key+candidate.Salt+"'", bytes.Repeat([]byte{0xa5}, 32))
	ctx, cancel := context.WithCancel(context.Background())
	_, err := collectWindowsKeyMaterial(ctx, regions, func(addr uint64, size int) ([]byte, error) {
		data, err := read(addr, size)
		cancel()
		return data, err
	}, &memoryReadBudget{remaining: 1 << 20})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation during a read was ignored")
	}
	material, err := collectWindowsKeyMaterial(context.Background(), regions, read, &memoryReadBudget{remaining: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	page[500] ^= 1
	if key, err := material.key(context.Background(), candidate.Salt, page); key != "" || !errors.Is(err, errKeyVerification) || strings.Contains(err.Error(), candidate.Key) {
		t.Fatal("failed key diagnostics exposed or accepted candidate material")
	}
}

func TestWindowsRuntimeNameAcrossAdjacentReadableRegions(t *testing.T) {
	candidate, page := memoryPage(t)
	_, read, _ := windowsMemoryFixture(t, "x'"+candidate.Key+candidate.Salt+"'", bytes.Repeat([]byte{0xa5}, 32))
	// VirtualQueryEx regions describe page attributes, not object boundaries.
	// Both ranges are readable and exactly adjacent; split inside the name.
	regions := []memoryRegion{{0x10000, 0x10a}, {0x1010a, 8192 - 0x10a}}
	material, err := collectWindowsKeyMaterial(context.Background(), regions, read, &memoryReadBudget{remaining: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if key, err := material.key(context.Background(), candidate.Salt, page); err != nil || key != candidate.Key {
		t.Fatal("runtime name split across adjacent readable regions was missed")
	}
}

func TestWindowsLegacyKeyAcrossAdjacentReadableRegions(t *testing.T) {
	candidate, page := memoryPage(t)
	_, read, data := windowsMemoryFixture(t, "", []byte{0xa5})
	clear(data[0x100:0x120])
	copy(data[0x1000:], "x'"+candidate.Key+candidate.Salt+"'")
	regions := []memoryRegion{{0x10000, 0x1020}, {0x11020, 8192 - 0x1020}}
	material, err := collectWindowsKeyMaterial(context.Background(), regions, read, &memoryReadBudget{remaining: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if key, err := material.key(context.Background(), candidate.Salt, page); err != nil || key != candidate.Key {
		t.Fatal("legacy key split across adjacent readable regions was missed")
	}
}

func TestMemoryWalkDoesNotJoinNonAdjacentRegions(t *testing.T) {
	var joined []byte
	regions := []memoryRegion{{0x10000, 4}, {0x11000, 4}}
	read := func(addr uint64, _ int) ([]byte, error) {
		if addr == 0x10000 {
			return []byte("ABCD"), nil
		}
		return []byte("EFGH"), nil
	}
	err := walkMemory(context.Background(), regions, read, &memoryReadBudget{remaining: 100}, 4, 3,
		func(_ uint64, data []byte) error { joined = append(joined, data...); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if string(joined) != "ABCDEFGH" {
		t.Fatal("scanner spliced bytes across an unmapped gap")
	}
}

func TestMemoryEnumerationDoesNotHideQueryFailures(t *testing.T) {
	denied := errors.New("synthetic access denied")
	for _, failureAt := range []uint64{0, 0x20000} {
		_, err := enumerateMemoryRegions(context.Background(), func(address uint64) (memoryRegionInfo, error) {
			if address == failureAt {
				return memoryRegionInfo{}, denied
			}
			return memoryRegionInfo{memoryRegion{address, 0x10000}, true}, nil
		})
		if !errors.Is(err, denied) {
			t.Fatal("memory query failure was misreported as a complete enumeration")
		}
	}
}

func TestMemoryEnumerationRecognizesEndAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	regions, err := enumerateMemoryRegions(ctx, func(address uint64) (memoryRegionInfo, error) {
		if address == 0 {
			return memoryRegionInfo{memoryRegion{0, 0x10000}, false}, nil
		}
		if address == 0x10000 {
			return memoryRegionInfo{memoryRegion{address, 0x1000}, true}, nil
		}
		return memoryRegionInfo{}, errMemoryRegionEnd
	})
	if err != nil || len(regions) != 1 || regions[0].base != 0x10000 || regions[0].size != 0x1000 {
		t.Fatal("normal end lost readable memory regions")
	}
	_, err = enumerateMemoryRegions(ctx, func(address uint64) (memoryRegionInfo, error) {
		cancel()
		return memoryRegionInfo{memoryRegion{address, 0x10000}, true}, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("enumeration continued after cancellation")
	}
}

func TestMemoryEnumerationRejectsNonAdvancingOrOverflowedRegions(t *testing.T) {
	for _, region := range []memoryRegion{{0, 0}, {0, 1}, {^uint64(0) - 10, 20}} {
		_, err := enumerateMemoryRegions(context.Background(), func(uint64) (memoryRegionInfo, error) { return memoryRegionInfo{region, true}, nil })
		if !errors.Is(err, errMemoryRead) {
			t.Fatal("invalid native region metadata was accepted as successful enumeration")
		}
	}
}
