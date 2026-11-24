package media

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"wxview/internal/app"
)

const (
	wechatV2HeaderSize       = 0x0f
	wechatV2CiphertextOffset = 0x0f
	wechatV2CiphertextEnd    = 0x1f
	wechatV2DatOverhead      = 31
)

var (
	wechatV2Magic = []byte{0x07, 0x08, 0x56, 0x32, 0x08, 0x07}
	kvcommPattern = regexp.MustCompile(`(?i)^key_(\d+)_.+\.statistic$`)
)

var decodedCacheManifests = struct {
	sync.Mutex
	byDir map[string]decodedCacheManifest
}{byDir: map[string]decodedCacheManifest{}}

type Info struct {
	Kind                string `json:"kind"`
	Status              string `json:"status"`
	Path                string `json:"path,omitempty"`
	SourcePath          string `json:"source_path,omitempty"`
	Decoded             bool   `json:"decoded"`
	Thumbnail           bool   `json:"thumbnail"`
	ThumbnailPath       string `json:"thumbnail_path,omitempty"`
	ThumbnailSourcePath string `json:"thumbnail_source_path,omitempty"`
	ThumbnailDecoded    bool   `json:"thumbnail_decoded,omitempty"`
	Width               int    `json:"width,omitempty"`
	Height              int    `json:"height,omitempty"`
	Reason              string `json:"reason,omitempty"`
}

type Resolver struct {
	Context     context.Context
	DataDir     string
	CacheDir    string
	ResourceDBs []string
	keys        imageKeyMaterial
}

type decodedCacheManifest struct {
	Entries map[string]decodedCacheEntry `json:"entries"`
}

type decodedCacheEntry struct {
	SourcePath    string `json:"source_path"`
	SourceSize    int64  `json:"source_size"`
	SourceMTimeNS int64  `json:"source_mtime_ns"`
	CacheKey      string `json:"cache_key"`
	Ext           string `json:"ext"`
	CachedPath    string `json:"cached_path"`
	UpdatedAt     string `json:"updated_at"`
}

type imageKeyMaterial struct {
	KVCommCodes []uint32
	SourceDirs  []string
}

type candidate struct {
	path      string
	thumbnail bool
}

type imageSizeHints struct {
	main  map[uint64]bool
	hd    uint64
	thumb uint64
}

func NewResolver(dataDir string, cacheDir string, resourceDBs ...string) Resolver {
	return Resolver{
		DataDir:     dataDir,
		CacheDir:    cacheDir,
		ResourceDBs: unique(resourceDBs),
		keys:        scanImageKeyMaterial(dataDir),
	}
}

func (r Resolver) Resolve(kind string, chatUsername string, localID int64, serverID int64, createTime int64, rawType int64, content string, isChatroom bool) Info {
	switch kind {
	case "image":
		return r.ResolveImage(chatUsername, localID, createTime, rawType, content, isChatroom)
	case "video":
		return r.ResolveVideo(chatUsername, localID, createTime, rawType, content, isChatroom)
	case "file":
		return r.ResolveFile(chatUsername, localID, createTime, rawType, content, isChatroom)
	case "voice":
		return r.ResolveVoice(chatUsername, localID, serverID, rawType)
	default:
		return Info{}
	}
}

func (r Resolver) ResolveImage(chatUsername string, localID int64, createTime int64, rawType int64, content string, isChatroom bool) Info {
	if int64(uint64(rawType)&0xffffffff) != 3 {
		return notFound("image", "not an image message")
	}
	body := messageBody(content, isChatroom)
	found, ok := resolveImagePath(r.DataDir, body, createTime, chatUsername, localID)
	if !ok {
		return notFound("image", "local image file not found")
	}
	if _, err := os.Stat(found.path); err != nil {
		return notFound("image", "local image file not found")
	}
	if !strings.HasSuffix(strings.ToLower(found.path), ".dat") {
		return withImageDimensions(resolved("image", found.path, found.path, false, found.thumbnail))
	}
	if out, ok := r.lookupDecodedCache(found.path); ok {
		return withImageDimensions(resolved("image", out, found.path, true, found.thumbnail))
	}
	out, err := r.decryptImageToCache(found.path)
	if err == nil {
		return withImageDimensions(resolved("image", out, found.path, true, found.thumbnail))
	}
	if !found.thumbnail {
		if thumb, ok := thumbnailVariant(found.path); ok && thumb != found.path {
			if out, ok := r.lookupDecodedCache(thumb); ok {
				return withImageDimensions(resolved("image", out, thumb, true, true))
			}
			if out, thumbErr := r.decryptImageToCache(thumb); thumbErr == nil {
				return withImageDimensions(resolved("image", out, thumb, true, true))
			}
		}
	}
	return decryptFailed("image", found.path, err.Error(), found.thumbnail)
}

func (i Info) Summary(localID int64) string {
	switch i.Status {
	case "resolved":
		if i.Path == "" {
			return fmt.Sprintf("[图片] local_id=%d (已解析但路径缺失)", localID)
		}
		text := "[图片] " + i.Path
		if i.Thumbnail {
			text += " (缩略图)"
		}
		return text
	case "not_found":
		return fmt.Sprintf("[图片] local_id=%d (未找到本地图片文件)", localID)
	case "decrypt_failed":
		return fmt.Sprintf("[图片] local_id=%d (无法解密)", localID)
	default:
		return fmt.Sprintf("[图片] local_id=%d", localID)
	}
}

func resolved(kind string, path string, sourcePath string, decoded bool, thumbnail bool) Info {
	return Info{Kind: kind, Status: "resolved", Path: path, SourcePath: sourcePath, Decoded: decoded, Thumbnail: thumbnail}
}

func withImageDimensions(info Info) Info {
	width, height, ok := imageDimensions(info.Path)
	if ok {
		info.Width = width
		info.Height = height
	}
	return info
}

func imageDimensions(path string) (int, int, bool) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, false
	}
	defer file.Close()
	cfg, _, err := image.DecodeConfig(file)
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return 0, 0, false
	}
	return cfg.Width, cfg.Height, true
}

func notFound(kind string, reason string) Info {
	return Info{Kind: kind, Status: "not_found", Decoded: false, Thumbnail: false, Reason: reason}
}

func decryptFailed(kind string, sourcePath string, reason string, thumbnail bool) Info {
	return Info{Kind: kind, Status: "decrypt_failed", SourcePath: sourcePath, Decoded: false, Thumbnail: thumbnail, Reason: reason}
}

func MediaCacheDir(account string) (string, error) {
	return app.CacheDBPath(account, "media")
}

func resolveImagePath(dataDir string, content string, createTime int64, chatUsername string, localID int64) (candidate, bool) {
	for _, base := range mediaBases(dataDir) {
		if c, ok := resolveImagePathMacOS4(base, createTime, chatUsername, localID); ok {
			return c, true
		}
	}
	return resolveImagePathLegacy(dataDir, content, createTime, chatUsername)
}

func mediaBases(dataDir string) []string {
	var bases []string
	if parent := filepath.Dir(dataDir); parent != "." && parent != dataDir {
		bases = append(bases, parent)
	}
	bases = append(bases, dataDir)
	return bases
}

func resolveImagePathMacOS4(wechatBase string, createTime int64, chatUsername string, localID int64) (candidate, bool) {
	if chatUsername == "" || createTime <= 0 || localID <= 0 {
		return candidate{}, false
	}
	chatHash := md5Hex(chatUsername)
	imageDir := filepath.Join(wechatBase, "Message", "MessageTemp", chatHash, "Image")
	if info, err := os.Stat(imageDir); err != nil || !info.IsDir() {
		return candidate{}, false
	}

	ts := strconv.FormatInt(createTime, 10)
	exact := filepath.Join(imageDir, fmt.Sprintf("%d%s_.pic_thumb.jpg", localID, ts))
	if fileExists(exact) {
		return candidate{path: exact, thumbnail: true}, true
	}

	var best *candidate
	entries, err := os.ReadDir(imageDir)
	if err != nil {
		return candidate{}, false
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.Contains(entry.Name(), ts) {
			continue
		}
		lower := strings.ToLower(entry.Name())
		if !isSupportedImageName(lower) && !strings.HasSuffix(lower, ".dat") {
			continue
		}
		c := candidate{path: filepath.Join(imageDir, entry.Name()), thumbnail: isThumbnailName(lower)}
		if !c.thumbnail {
			return c, true
		}
		if best == nil {
			best = &c
		}
	}
	if best != nil {
		return *best, true
	}
	return candidate{}, false
}

func resolveImagePathLegacy(dataDir string, content string, createTime int64, chatUsername string) (candidate, bool) {
	if createTime <= 0 {
		return candidate{}, false
	}
	wechatBase := filepath.Dir(dataDir)
	attachDir := filepath.Join(wechatBase, "msg", "attach")
	if info, err := os.Stat(attachDir); err != nil || !info.IsDir() {
		return candidate{}, false
	}
	datePrefix := time.Unix(createTime, 0).Format("2006-01")
	targetHash := ""
	if chatUsername != "" {
		hash := md5Hex(chatUsername)
		if info, err := os.Stat(filepath.Join(attachDir, hash)); err == nil && info.IsDir() {
			targetHash = hash
		}
	}
	var dirs []string
	if targetHash != "" {
		dirs = append(dirs, targetHash)
	}
	entries, err := os.ReadDir(attachDir)
	if err != nil {
		return candidate{}, false
	}
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != targetHash {
			dirs = append(dirs, entry.Name())
		}
	}

	hints := parseImageSizeHints(content)
	bestScore := -1
	bestPath := ""
	for _, dir := range dirs {
		imageDir := filepath.Join(attachDir, dir, datePrefix, "Img")
		entries, err := os.ReadDir(imageDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			path := filepath.Join(imageDir, entry.Name())
			score := scoreImageCandidate(path, hints)
			if score > bestScore {
				bestScore = score
				bestPath = path
			}
		}
	}
	if bestPath == "" {
		return candidate{}, false
	}
	path := promoteImageVariant(bestPath)
	return candidate{path: path, thumbnail: isThumbnailPath(path)}, true
}

func (r Resolver) decryptImageToCache(path string) (string, error) {
	bytes, ext, cacheKey, err := decryptImageBytes(path, r.keys)
	if err != nil {
		return "", err
	}
	return r.writeDecodedToCache(path, bytes, ext, cacheKey)
}

func (r Resolver) writeDecodedToCache(path string, bytes []byte, ext string, cacheKey string) (string, error) {
	if err := os.MkdirAll(r.CacheDir, 0o700); err != nil {
		return "", err
	}
	if err := app.ChownForSudo(r.CacheDir); err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte(path))
	h.Write([]byte(fmt.Sprintf(":%d:%d:%s", info.Size(), info.ModTime().UnixNano(), cacheKey)))
	digest := hex.EncodeToString(h.Sum(nil))
	outPath := filepath.Join(r.CacheDir, digest[:24]+"."+ext)
	if fileExists(outPath) {
		r.rememberDecodedCache(path, outPath, ext, cacheKey, info)
		return outPath, nil
	}
	if err := r.writeBytesToCache(outPath, bytes); err != nil {
		return "", err
	}
	r.rememberDecodedCache(path, outPath, ext, cacheKey, info)
	return outPath, nil
}

func (r Resolver) lookupDecodedCache(path string) (string, bool) {
	if !strings.HasSuffix(strings.ToLower(path), ".dat") {
		return "", false
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	manifest, err := r.loadDecodedCacheManifest()
	if err != nil || len(manifest.Entries) == 0 {
		return "", false
	}
	entry, ok := manifest.Entries[decodedCacheSourceKey(path)]
	if !ok {
		return "", false
	}
	if entry.SourceSize != info.Size() || entry.SourceMTimeNS != info.ModTime().UnixNano() || entry.CachedPath == "" {
		return "", false
	}
	if !fileExists(entry.CachedPath) {
		return "", false
	}
	return entry.CachedPath, true
}

func (r Resolver) rememberDecodedCache(path string, outPath string, ext string, cacheKey string, info os.FileInfo) {
	if !strings.HasSuffix(strings.ToLower(path), ".dat") || outPath == "" || info == nil {
		return
	}
	manifest, _ := r.loadDecodedCacheManifest()
	if manifest.Entries == nil {
		manifest.Entries = map[string]decodedCacheEntry{}
	}
	sourcePath := decodedCacheSourceKey(path)
	manifest.Entries[sourcePath] = decodedCacheEntry{
		SourcePath:    sourcePath,
		SourceSize:    info.Size(),
		SourceMTimeNS: info.ModTime().UnixNano(),
		CacheKey:      cacheKey,
		Ext:           ext,
		CachedPath:    outPath,
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}
	_ = r.writeDecodedCacheManifest(manifest)
}

func (r Resolver) loadDecodedCacheManifest() (decodedCacheManifest, error) {
	cacheDir := filepath.Clean(r.CacheDir)
	decodedCacheManifests.Lock()
	if manifest, ok := decodedCacheManifests.byDir[cacheDir]; ok {
		decodedCacheManifests.Unlock()
		return manifest, nil
	}
	decodedCacheManifests.Unlock()

	path := r.decodedCacheManifestPath()
	data, err := os.ReadFile(path)
	if err != nil {
		manifest := decodedCacheManifest{Entries: map[string]decodedCacheEntry{}}
		if os.IsNotExist(err) {
			cacheDecodedCacheManifest(cacheDir, manifest)
		}
		return manifest, err
	}
	var manifest decodedCacheManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return decodedCacheManifest{Entries: map[string]decodedCacheEntry{}}, err
	}
	if manifest.Entries == nil {
		manifest.Entries = map[string]decodedCacheEntry{}
	}
	cacheDecodedCacheManifest(cacheDir, manifest)
	return manifest, nil
}

func (r Resolver) writeDecodedCacheManifest(manifest decodedCacheManifest) error {
	if err := os.MkdirAll(r.CacheDir, 0o700); err != nil {
		return err
	}
	if err := app.ChownForSudo(r.CacheDir); err != nil {
		return err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(r.CacheDir, ".decoded-cache-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write([]byte("\n")); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, r.decodedCacheManifestPath()); err != nil {
		return err
	}
	_ = app.ChownForSudo(r.decodedCacheManifestPath())
	cacheDecodedCacheManifest(filepath.Clean(r.CacheDir), manifest)
	return nil
}

func cacheDecodedCacheManifest(cacheDir string, manifest decodedCacheManifest) {
	if manifest.Entries == nil {
		manifest.Entries = map[string]decodedCacheEntry{}
	}
	decodedCacheManifests.Lock()
	decodedCacheManifests.byDir[cacheDir] = manifest
	decodedCacheManifests.Unlock()
}

func (r Resolver) decodedCacheManifestPath() string {
	return filepath.Join(r.CacheDir, ".decoded-cache.json")
}

func decodedCacheSourceKey(path string) string {
	return filepath.Clean(path)
}

func (r Resolver) writeBytesToCache(outPath string, bytes []byte) error {
	tmp, err := os.CreateTemp(r.CacheDir, filepath.Base(outPath)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	if _, err := tmp.Write(bytes); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, outPath); err != nil {
		return err
	}
	_ = app.ChownForSudo(outPath)
	return nil
}

func decryptImageBytes(path string, keys imageKeyMaterial) ([]byte, string, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", "", err
	}
	if ext := detectImageExtension(data); ext != "" {
		return data, ext, "raw", nil
	}
	if isWechatV2Dat(data) {
		if bytes, ext, cacheKey, ok := decryptWechatV2Dat(data, path, keys); ok {
			return bytes, ext, cacheKey, nil
		}
	}
	if key, ext, ok := detectXORImageKey(data); ok {
		return xorBytes(data, key), ext, fmt.Sprintf("xor:%d", key), nil
	}
	return nil, "", "", fmt.Errorf("unsupported or unknown image encryption")
}

func decryptWechatV2Dat(data []byte, path string, keys imageKeyMaterial) ([]byte, string, string, bool) {
	if len(data) < wechatV2CiphertextEnd {
		return nil, "", "", false
	}
	ciphertext := data[wechatV2CiphertextOffset:wechatV2CiphertextEnd]
	for _, code := range keys.KVCommCodes {
		xorKey := byte(code & 0xff)
		for _, wxid := range collectWXIDCandidates(path) {
			aesKey := deriveV2AESKey(code, wxid)
			if !verifyV2AESKey(aesKey, ciphertext) {
				continue
			}
			decrypted, ok := decryptWechatV2DatWithKeys(data, xorKey, []byte(aesKey))
			if !ok {
				continue
			}
			decrypted = unwrapWXGF(decrypted)
			ext := detectImageExtension(decrypted)
			if ext == "" {
				continue
			}
			return decrypted, ext, fmt.Sprintf("v2:%d:%s:%s:%d", code, wxid, aesKey, xorKey), true
		}
	}
	return nil, "", "", false
}

func decryptWechatV2DatWithKeys(data []byte, xorKey byte, aesKey []byte) ([]byte, bool) {
	if len(data) < wechatV2HeaderSize || len(aesKey) < aes.BlockSize {
		return nil, false
	}
	header := data[:wechatV2HeaderSize]
	payload := data[wechatV2HeaderSize:]
	aesSize, ok := readI32LE(header, 6)
	if !ok {
		return nil, false
	}
	xorSize, ok := readI32LE(header, 10)
	if !ok {
		return nil, false
	}
	alignedAESSize := aesSize
	if rem := aesSize % aes.BlockSize; rem == 0 {
		alignedAESSize += aes.BlockSize
	} else {
		alignedAESSize += aes.BlockSize - rem
	}
	if alignedAESSize > len(payload) || xorSize > len(payload)-alignedAESSize {
		return nil, false
	}
	aesData := payload[:alignedAESSize]
	remaining := payload[alignedAESSize:]
	var plainAES []byte
	if len(aesData) > 0 {
		plain, ok := aesECBDecrypt(aesKey[:aes.BlockSize], aesData)
		if !ok {
			return nil, false
		}
		plainAES, ok = removePKCS7Padding(plain)
		if !ok {
			return nil, false
		}
	}
	out := append([]byte{}, plainAES...)
	if xorSize > 0 {
		rawLen := len(remaining) - xorSize
		if rawLen < 0 {
			return nil, false
		}
		out = append(out, remaining[:rawLen]...)
		out = append(out, xorBytes(remaining[rawLen:], xorKey)...)
		return out, true
	}
	out = append(out, remaining...)
	return out, true
}

func verifyV2AESKey(aesKey string, ciphertext []byte) bool {
	decrypted, ok := aesECBDecrypt([]byte(aesKey)[:aes.BlockSize], ciphertext)
	return ok && (detectImageExtension(decrypted) != "" || bytes.HasPrefix(decrypted, []byte("wxgf")))
}

func aesECBDecrypt(key []byte, data []byte) ([]byte, bool) {
	if len(data)%aes.BlockSize != 0 {
		return nil, false
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, false
	}
	out := append([]byte{}, data...)
	for i := 0; i < len(out); i += aes.BlockSize {
		block.Decrypt(out[i:i+aes.BlockSize], out[i:i+aes.BlockSize])
	}
	return out, true
}

func unwrapWXGF(data []byte) []byte {
	if len(data) < 20 || !bytes.HasPrefix(data, []byte("wxgf")) {
		return data
	}
	limit := len(data) - 12
	if limit > 4096 {
		limit = 4096
	}
	for i := 4; i < limit; i++ {
		if detectImageExtension(data[i:]) != "" {
			return data[i:]
		}
	}
	if jpg := convertHEVCToJPG(data); len(jpg) > 0 {
		return jpg
	}
	return data
}

func convertHEVCToJPG(data []byte) []byte {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil
	}
	in, err := os.CreateTemp("", "wxview-wxgf-*.hevc")
	if err != nil {
		return nil
	}
	inPath := in.Name()
	outPath := strings.TrimSuffix(inPath, ".hevc") + ".jpg"
	defer func() {
		_ = os.Remove(inPath)
		_ = os.Remove(outPath)
	}()
	if _, err := in.Write(data); err != nil {
		_ = in.Close()
		return nil
	}
	if err := in.Close(); err != nil {
		return nil
	}
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y", "-i", inPath, "-frames:v", "1", outPath)
	if err := cmd.Run(); err != nil {
		return nil
	}
	out, err := os.ReadFile(outPath)
	if err != nil || detectImageExtension(out) != "jpg" {
		return nil
	}
	return out
}

func isWechatV2Dat(data []byte) bool {
	return len(data) >= wechatV2CiphertextEnd && bytes.HasPrefix(data, wechatV2Magic)
}

func deriveV2AESKey(code uint32, wxid string) string {
	return md5Hex(fmt.Sprintf("%d%s", code, wxid))[:16]
}

func readI32LE(data []byte, offset int) (int, bool) {
	if offset < 0 || offset+4 > len(data) {
		return 0, false
	}
	value := int(int32(data[offset]) | int32(data[offset+1])<<8 | int32(data[offset+2])<<16 | int32(data[offset+3])<<24)
	return value, value >= 0
}

func removePKCS7Padding(data []byte) ([]byte, bool) {
	if len(data) == 0 {
		return nil, false
	}
	pad := int(data[len(data)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(data) {
		return nil, false
	}
	for _, b := range data[len(data)-pad:] {
		if int(b) != pad {
			return nil, false
		}
	}
	return data[:len(data)-pad], true
}

func detectXORImageKey(data []byte) (byte, string, bool) {
	signatures := []struct {
		ext   string
		parts []signaturePart
	}{
		{"jpg", []signaturePart{{0, []byte{0xff, 0xd8, 0xff}}}},
		{"png", []signaturePart{{0, []byte("\x89PNG\r\n\x1a\n")}, {12, []byte("IHDR")}}},
		{"gif", []signaturePart{{0, []byte("GIF87a")}}},
		{"gif", []signaturePart{{0, []byte("GIF89a")}}},
		{"bmp", []signaturePart{{0, []byte("BM")}}},
		{"webp", []signaturePart{{0, []byte("RIFF")}, {8, []byte("WEBP")}}},
	}
	for _, sig := range signatures {
		if len(data) <= sig.parts[0].offset {
			continue
		}
		key := data[sig.parts[0].offset] ^ sig.parts[0].data[0]
		matched := true
		for _, part := range sig.parts {
			end := part.offset + len(part.data)
			if len(data) < end || !bytes.Equal(xorBytes(data[part.offset:end], key), part.data) {
				matched = false
				break
			}
		}
		if matched {
			sample := data
			if len(sample) > 64 {
				sample = sample[:64]
			}
			if detectImageExtension(xorBytes(sample, key)) != "" {
				return key, sig.ext, true
			}
		}
	}
	return 0, "", false
}

type signaturePart struct {
	offset int
	data   []byte
}

func xorBytes(data []byte, key byte) []byte {
	out := make([]byte, len(data))
	for i := range data {
		out[i] = data[i] ^ key
	}
	return out
}

func detectImageExtension(data []byte) string {
	switch {
	case len(data) >= 3 && bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}):
		return "jpg"
	case len(data) >= 8 && bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "png"
	case len(data) >= 6 && (bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a"))):
		return "gif"
	case validBMP(data):
		return "bmp"
	case validWebP(data):
		return "webp"
	default:
		return ""
	}
}

func validBMP(data []byte) bool {
	if len(data) < 26 || !bytes.HasPrefix(data, []byte("BM")) {
		return false
	}
	fileSize := readU32LE(data[2:6])
	pixelOffset := readU32LE(data[10:14])
	dibSize := readU32LE(data[14:18])
	width := int32(readU32LE(data[18:22]))
	height := int32(readU32LE(data[22:26]))
	validDIB := dibSize == 12 || dibSize == 40 || dibSize == 52 || dibSize == 56 || dibSize == 108 || dibSize == 124
	return fileSize >= 26 &&
		fileSize <= 1024*1024*1024 &&
		pixelOffset >= 26 &&
		pixelOffset <= fileSize &&
		validDIB &&
		width != 0 &&
		height != 0
}

func validWebP(data []byte) bool {
	if len(data) < 16 || !bytes.Equal(data[:4], []byte("RIFF")) || !bytes.Equal(data[8:12], []byte("WEBP")) {
		return false
	}
	riffSize := readU32LE(data[4:8])
	chunk := string(data[12:16])
	return riffSize > 4 && (chunk == "VP8 " || chunk == "VP8L" || chunk == "VP8X")
}

func readU32LE(data []byte) uint32 {
	if len(data) < 4 {
		return 0
	}
	return uint32(data[0]) | uint32(data[1])<<8 | uint32(data[2])<<16 | uint32(data[3])<<24
}

func scanImageKeyMaterial(dataDir string) imageKeyMaterial {
	codeSet := map[uint32]bool{}
	var sourceDirs []string
	for _, dir := range kvcommCandidateDirs(dataDir) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		found := false
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			match := kvcommPattern.FindStringSubmatch(entry.Name())
			if len(match) != 2 {
				continue
			}
			code, err := strconv.ParseUint(match[1], 10, 32)
			if err != nil {
				continue
			}
			codeSet[uint32(code)] = true
			found = true
		}
		if found {
			sourceDirs = append(sourceDirs, dir)
		}
	}
	codes := make([]uint32, 0, len(codeSet))
	for code := range codeSet {
		codes = append(codes, code)
	}
	sort.Slice(codes, func(i, j int) bool { return codes[i] < codes[j] })
	return imageKeyMaterial{KVCommCodes: codes, SourceDirs: sourceDirs}
}

func kvcommCandidateDirs(dataDir string) []string {
	var out []string
	if home, err := app.HomeDir(); err == nil {
		out = append(out,
			filepath.Join(home, "Library", "Containers", "com.tencent.xinWeChat", "Data", "Documents", "app_data", "net", "kvcomm"),
			filepath.Join(home, "Library", "Containers", "com.tencent.xinWeChat", "Data", "Library", "Application Support", "com.tencent.xinWeChat", "xwechat", "net", "kvcomm"),
			filepath.Join(home, "Library", "Containers", "com.tencent.xinWeChat", "Data", "Library", "Application Support", "com.tencent.xinWeChat", "net", "kvcomm"),
			filepath.Join(home, "Library", "Containers", "com.tencent.xinWeChat", "Data", "Documents", "xwechat", "net", "kvcomm"),
		)
	}
	normalized := filepath.ToSlash(filepath.Clean(dataDir))
	if idx := strings.Index(normalized, "/xwechat_files"); idx >= 0 {
		out = append(out, filepath.Join(filepath.FromSlash(normalized[:idx]), "app_data", "net", "kvcomm"))
	}
	return unique(out)
}

func collectWXIDCandidates(path string) []string {
	var out []string
	for _, part := range strings.Split(filepath.ToSlash(filepath.Clean(path)), "/") {
		if strings.HasPrefix(part, "wxid_") {
			pushWXIDCandidate(&out, part)
		}
	}
	return out
}

func pushWXIDCandidate(out *[]string, value string) {
	value = strings.TrimSpace(value)
	if value == "" || contains(*out, value) {
		return
	}
	*out = append(*out, value)
	if strings.Contains(value, "_") {
		if idx := strings.LastIndex(value, "_"); idx > 0 {
			withoutLast := value[:idx]
			if withoutLast != "" && !contains(*out, withoutLast) {
				*out = append(*out, withoutLast)
			}
		}
		if prefix, _, ok := strings.Cut(value, "_"); ok && prefix != "" && !contains(*out, prefix) {
			*out = append(*out, prefix)
		}
	}
}

func parseImageSizeHints(content string) imageSizeHints {
	hints := imageSizeHints{main: map[uint64]bool{}}
	xmlText := extractXMLContent(content)
	if xmlText == "" {
		return hints
	}
	decoder := xml.NewDecoder(strings.NewReader(xmlText))
	for {
		token, err := decoder.Token()
		if err != nil {
			return hints
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "img" {
			continue
		}
		attr := func(name string) uint64 {
			for _, a := range start.Attr {
				if a.Name.Local == name {
					v, _ := strconv.ParseUint(strings.TrimSpace(a.Value), 10, 64)
					return v
				}
			}
			return 0
		}
		for _, v := range []uint64{attr("length"), attr("hevc_mid_size")} {
			if v > 0 {
				hints.main[v] = true
			}
		}
		hints.hd = attr("hdlength")
		hints.thumb = attr("cdnthumblength")
		return hints
	}
}

func scoreImageCandidate(path string, hints imageSizeHints) int {
	name := strings.ToLower(filepath.Base(path))
	if hints.hd == 0 && len(hints.main) == 0 && hints.thumb == 0 {
		if strings.HasSuffix(name, "_h.dat") {
			return 3
		}
		if !strings.HasSuffix(name, "_t.dat") {
			return 2
		}
		return 1
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	size := uint64(info.Size())
	withoutOverhead := size
	if withoutOverhead >= wechatV2DatOverhead {
		withoutOverhead -= wechatV2DatOverhead
	}
	switch {
	case hints.hd > 0 && (size == hints.hd || withoutOverhead == hints.hd):
		if strings.HasSuffix(name, "_h.dat") {
			return 50
		}
		return 45
	case hints.main[size] || hints.main[withoutOverhead]:
		if !strings.HasSuffix(name, "_t.dat") {
			return 40
		}
		return 30
	case hints.thumb > 0 && (size == hints.thumb || withoutOverhead == hints.thumb):
		if strings.HasSuffix(name, "_t.dat") {
			return 10
		}
		return 5
	default:
		return 0
	}
}

func promoteImageVariant(path string) string {
	lower := strings.ToLower(path)
	if !strings.HasSuffix(lower, "_t.dat") {
		return path
	}
	base := path[:len(path)-6]
	for _, candidate := range []string{base + "_h.dat", base + ".dat"} {
		if fileExists(candidate) {
			return candidate
		}
	}
	return path
}

func thumbnailVariant(path string) (string, bool) {
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, "_t.dat"):
		return path, fileExists(path)
	case strings.HasSuffix(lower, "_h.dat"):
		candidate := path[:len(path)-6] + "_t.dat"
		return candidate, fileExists(candidate)
	case strings.HasSuffix(lower, ".dat"):
		candidate := path[:len(path)-4] + "_t.dat"
		return candidate, fileExists(candidate)
	default:
		return "", false
	}
}

func messageBody(content string, isChatroom bool) string {
	if isChatroom {
		if _, body, ok := strings.Cut(content, ":\n"); ok {
			return body
		}
	}
	return content
}

func extractXMLContent(content string) string {
	idx := strings.Index(content, "<")
	if idx < 0 {
		return content
	}
	return content[idx:]
}

func isSupportedImageName(lower string) bool {
	return strings.HasSuffix(lower, ".jpg") ||
		strings.HasSuffix(lower, ".jpeg") ||
		strings.HasSuffix(lower, ".png") ||
		strings.HasSuffix(lower, ".gif") ||
		strings.HasSuffix(lower, ".bmp") ||
		strings.HasSuffix(lower, ".webp")
}

func isThumbnailName(lower string) bool {
	return strings.HasSuffix(lower, "_t.dat") || strings.Contains(lower, "thumb") || strings.HasSuffix(lower, ".pic_thumb.jpg")
}

func isThumbnailPath(path string) bool {
	return isThumbnailName(strings.ToLower(filepath.Base(path)))
}

func md5Hex(value string) string {
	sum := md5.Sum([]byte(value))
	return hex.EncodeToString(sum[:])
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func unique(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func (r Resolver) context() context.Context {
	if r.Context != nil {
		return r.Context
	}
	return context.Background()
}
