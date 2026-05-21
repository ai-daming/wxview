package media

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type videoCandidate struct {
	path                string
	thumbnailPath       string
	thumbnailSourcePath string
	thumbnailDecoded    bool
	score               int
}

type videoHints struct {
	keys        []string
	sizes       map[int64]bool
	thumbSize   int64
	thumbWidth  int
	thumbHeight int
}

type videoEnvelope struct {
	Video videoXML `xml:"videomsg"`
}

type videoXML struct {
	MD5             string `xml:"md5,attr"`
	NewMD5          string `xml:"newmd5,attr"`
	RawMD5          string `xml:"rawmd5,attr"`
	OriginSourceMD5 string `xml:"originsourcemd5,attr"`
	Length          string `xml:"length,attr"`
	RawLength       string `xml:"rawlength,attr"`
	CDNThumbLength  string `xml:"cdnthumblength,attr"`
	CDNThumbWidth   string `xml:"cdnthumbwidth,attr"`
	CDNThumbHeight  string `xml:"cdnthumbheight,attr"`
}

func (r Resolver) ResolveVideo(chatUsername string, localID int64, createTime int64, rawType int64, content string, isChatroom bool) Info {
	if int64(uint64(rawType)&0xffffffff) != 43 {
		return notFound("video", "not a video message")
	}
	body := messageBody(content, isChatroom)
	hints := parseVideoHints(body)
	found, ok := r.resolveVideoPath(body, createTime, chatUsername, localID, hints)
	if !ok {
		return notFound("video", "local video file not found")
	}
	if _, err := os.Stat(found.path); err != nil {
		return notFound("video", "local video file not found")
	}

	path := found.path
	sourcePath := found.path
	decoded := false
	if strings.HasSuffix(strings.ToLower(path), ".dat") {
		if out, ok := r.lookupDecodedCache(path); ok {
			path = out
			decoded = true
		} else {
			out, err := r.decryptVideoToCache(path)
			if err != nil {
				info := decryptFailed("video", sourcePath, err.Error(), false)
				r.attachVideoThumbnail(&info, found)
				return info
			}
			path = out
			decoded = true
		}
	}

	info := resolved("video", path, sourcePath, decoded, found.thumbnailPath != "")
	r.attachVideoThumbnail(&info, found)
	if info.Width == 0 && hints.thumbWidth > 0 {
		info.Width = hints.thumbWidth
	}
	if info.Height == 0 && hints.thumbHeight > 0 {
		info.Height = hints.thumbHeight
	}
	return info
}

func (r Resolver) resolveVideoPath(content string, createTime int64, chatUsername string, localID int64, hints videoHints) (videoCandidate, bool) {
	if c, ok := r.resolveVideoPathFromResources(hints); ok {
		return c, true
	}
	for _, base := range mediaBases(r.DataDir) {
		if c, ok := resolveVideoPathMacOS4(base, createTime, chatUsername, localID, hints); ok {
			return c, true
		}
	}
	return resolveVideoPathLegacy(r.DataDir, content, createTime, chatUsername, localID, hints)
}

func (r Resolver) resolveVideoPathFromResources(hints videoHints) (videoCandidate, bool) {
	files := queryResourceMedia(r.ResourceDBs, r.DataDir, "video", hints.keys)
	if len(files) == 0 {
		return videoCandidate{}, false
	}
	return chooseVideoCandidate(files, hints, 0, 0)
}

func resolveVideoPathMacOS4(wechatBase string, createTime int64, chatUsername string, localID int64, hints videoHints) (videoCandidate, bool) {
	if chatUsername == "" || createTime <= 0 || localID <= 0 {
		return videoCandidate{}, false
	}
	chatHash := md5Hex(chatUsername)
	videoDir := filepath.Join(wechatBase, "Message", "MessageTemp", chatHash, "Video")
	if c, ok := exactVideoCandidate(videoDir, createTime, localID); ok {
		return c, true
	}
	if c, ok := scanVideoDir(videoDir, hints, createTime, localID); ok {
		return c, true
	}
	return scanVideoDir(filepath.Join(wechatBase, "Message", "MessageTemp", chatHash, "File"), hints, createTime, localID)
}

func resolveVideoPathLegacy(dataDir string, _ string, createTime int64, chatUsername string, localID int64, hints videoHints) (videoCandidate, bool) {
	accountBase := filepath.Dir(dataDir)
	videoRoot := filepath.Join(accountBase, "msg", "video")
	chatHash := ""
	if chatUsername != "" {
		chatHash = md5Hex(chatUsername)
		if c, ok := scanVideoDir(filepath.Join(videoRoot, chatHash), hints, createTime, localID); ok {
			return c, true
		}
	}
	if len(hints.keys) == 0 {
		return videoCandidate{}, false
	}
	entries, err := os.ReadDir(videoRoot)
	if err != nil {
		return videoCandidate{}, false
	}
	var candidates []videoCandidate
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == chatHash {
			continue
		}
		if c, ok := scanVideoDir(filepath.Join(videoRoot, entry.Name()), hints, createTime, localID); ok {
			candidates = append(candidates, c)
		}
	}
	return bestVideoCandidate(candidates)
}

func exactVideoCandidate(dir string, createTime int64, localID int64) (videoCandidate, bool) {
	if dir == "" {
		return videoCandidate{}, false
	}
	prefixes := []string{
		fmt.Sprintf("%d%d", localID, createTime),
		fmt.Sprintf("%d_%d", localID, createTime),
	}
	for _, prefix := range prefixes {
		for _, ext := range []string{".mp4", ".mov", ".m4v", ".dat"} {
			path := filepath.Join(dir, prefix+ext)
			if fileExists(path) {
				c := videoCandidate{path: path, score: 1000}
				if thumb := thumbnailForVideoPath(path); thumb != "" {
					c.thumbnailPath = thumb
					c.thumbnailSourcePath = thumb
				}
				return c, true
			}
		}
	}
	return videoCandidate{}, false
}

func scanVideoDir(dir string, hints videoHints, createTime int64, localID int64) (videoCandidate, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return videoCandidate{}, false
	}
	files := make([]resourceMediaFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		lower := strings.ToLower(entry.Name())
		if !isSupportedVideoName(lower) && !isVideoThumbnailName(lower) && !strings.HasSuffix(lower, ".dat") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		files = append(files, resourceMediaFile{Name: entry.Name(), Size: info.Size(), ModifyTime: info.ModTime().Unix(), Path: path})
	}
	return chooseVideoCandidate(files, hints, createTime, localID)
}

func chooseVideoCandidate(files []resourceMediaFile, hints videoHints, createTime int64, localID int64) (videoCandidate, bool) {
	var mains []videoCandidate
	var thumbs []videoCandidate
	for _, file := range files {
		path := existingVideoPath(file.Path)
		if path == "" {
			continue
		}
		file.Path = path
		file.Name = filepath.Base(path)
		lower := strings.ToLower(file.Name)
		isThumb := isVideoThumbnailName(lower)
		if !isThumb && !isSupportedVideoName(lower) && !strings.HasSuffix(lower, ".dat") {
			continue
		}
		score := scoreVideoFile(file, hints, createTime, localID, isThumb)
		if score <= 0 {
			continue
		}
		c := videoCandidate{path: file.Path, score: score}
		if isThumb {
			thumbs = append(thumbs, c)
			continue
		}
		mains = append(mains, c)
	}
	main, ok := bestVideoCandidate(mains)
	if !ok {
		return videoCandidate{}, false
	}
	if thumb, ok := bestVideoCandidate(thumbs); ok {
		main.thumbnailPath = thumb.path
		main.thumbnailSourcePath = thumb.path
	} else if thumbPath := thumbnailForVideoPath(main.path); thumbPath != "" {
		main.thumbnailPath = thumbPath
		main.thumbnailSourcePath = thumbPath
	}
	return main, true
}

func existingVideoPath(path string) string {
	if fileExists(path) {
		return path
	}
	for _, candidate := range []string{
		path + ".mp4",
		path + "_thumb.jpg",
		path + ".thumb.jpg",
		path + ".jpg",
	} {
		if fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

func bestVideoCandidate(candidates []videoCandidate) (videoCandidate, bool) {
	if len(candidates) == 0 {
		return videoCandidate{}, false
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].path < candidates[j].path
	})
	return candidates[0], true
}

func scoreVideoFile(file resourceMediaFile, hints videoHints, createTime int64, localID int64, thumbnail bool) int {
	name := strings.ToLower(filepath.Base(file.Path))
	score := 0
	strong := false
	for _, key := range hints.keys {
		if key != "" && (strings.Contains(name, key) || strings.EqualFold(file.Key, key)) {
			score += 100
			strong = true
		}
	}
	if localID > 0 {
		localIDText := strconv.FormatInt(localID, 10)
		if strings.Contains(name, localIDText) {
			score += 20
			strong = true
		}
	}
	if createTime > 0 {
		ts := strconv.FormatInt(createTime, 10)
		if strings.Contains(name, ts) {
			score += 50
			strong = true
		}
	}
	if thumbnail {
		if hints.thumbSize > 0 && file.Size == hints.thumbSize {
			score += 70
			strong = true
		}
	} else {
		for size := range hints.sizes {
			if size > 0 && file.Size == size {
				score += 70
				strong = true
			}
		}
	}
	if !strong {
		return 0
	}
	if createTime > 0 && file.ModifyTime > 0 && absInt64(file.ModifyTime-createTime) <= int64(48*time.Hour/time.Second) {
		score += 10
	}
	return score
}

func (r Resolver) attachVideoThumbnail(info *Info, found videoCandidate) {
	if info == nil || found.thumbnailPath == "" {
		return
	}
	path := found.thumbnailPath
	sourcePath := firstNonEmptyString(found.thumbnailSourcePath, found.thumbnailPath)
	decoded := found.thumbnailDecoded
	if strings.HasSuffix(strings.ToLower(path), ".dat") {
		if out, ok := r.lookupDecodedCache(path); ok {
			path = out
			decoded = true
		} else {
			out, err := r.decryptImageToCache(path)
			if err != nil {
				info.ThumbnailSourcePath = sourcePath
				return
			}
			path = out
			decoded = true
		}
	}
	info.Thumbnail = true
	info.ThumbnailPath = path
	info.ThumbnailSourcePath = sourcePath
	info.ThumbnailDecoded = decoded
	if width, height, ok := imageDimensions(path); ok {
		info.Width = width
		info.Height = height
	}
}

func thumbnailForVideoPath(path string) string {
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for _, candidate := range []string{
		base + "_thumb.jpg",
		base + ".thumb.jpg",
		base + ".jpg",
		path + "_thumb.jpg",
	} {
		if fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

func parseVideoHints(content string) videoHints {
	hints := videoHints{sizes: map[int64]bool{}}
	xmlText := extractXMLContent(content)
	if xmlText == "" {
		return hints
	}
	var msg videoEnvelope
	if err := xml.Unmarshal([]byte(xmlText), &msg); err != nil {
		return hints
	}
	video := msg.Video
	hints.keys = cleanMediaKeys([]string{video.MD5, video.NewMD5, video.RawMD5, video.OriginSourceMD5})
	for _, value := range []string{video.Length, video.RawLength} {
		if parsed := parsePositiveInt(value); parsed > 0 {
			hints.sizes[parsed] = true
		}
	}
	hints.thumbSize = parsePositiveInt(video.CDNThumbLength)
	hints.thumbWidth = int(parsePositiveInt(video.CDNThumbWidth))
	hints.thumbHeight = int(parsePositiveInt(video.CDNThumbHeight))
	return hints
}

func (r Resolver) decryptVideoToCache(path string) (string, error) {
	bytes, ext, cacheKey, err := decryptVideoBytes(path)
	if err != nil {
		return "", err
	}
	return r.writeDecodedToCache(path, bytes, ext, cacheKey)
}

func decryptVideoBytes(path string) ([]byte, string, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", "", err
	}
	if ext := detectVideoExtension(data); ext != "" {
		return data, ext, "raw", nil
	}
	if key, ext, ok := detectXORVideoKey(data); ok {
		return xorBytes(data, key), ext, fmt.Sprintf("xor:%d", key), nil
	}
	return nil, "", "", fmt.Errorf("unsupported or unknown video encryption")
}

func detectXORVideoKey(data []byte) (byte, string, bool) {
	signatures := []struct {
		ext   string
		parts []signaturePart
	}{
		{"mp4", []signaturePart{{4, []byte("ftyp")}}},
		{"webm", []signaturePart{{0, []byte{0x1a, 0x45, 0xdf, 0xa3}}}},
		{"avi", []signaturePart{{0, []byte("RIFF")}, {8, []byte("AVI ")}}},
		{"mpg", []signaturePart{{0, []byte{0x00, 0x00, 0x01, 0xba}}}},
		{"mpg", []signaturePart{{0, []byte{0x00, 0x00, 0x01, 0xb3}}}},
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
			if len(sample) > 128 {
				sample = sample[:128]
			}
			if detectVideoExtension(xorBytes(sample, key)) != "" {
				return key, sig.ext, true
			}
		}
	}
	return 0, "", false
}

func detectVideoExtension(data []byte) string {
	switch {
	case len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp")):
		return "mp4"
	case len(data) >= 4 && bytes.Equal(data[:4], []byte{0x1a, 0x45, 0xdf, 0xa3}):
		return "webm"
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("AVI ")):
		return "avi"
	case len(data) >= 4 && bytes.Equal(data[:4], []byte{0x00, 0x00, 0x01, 0xba}):
		return "mpg"
	case len(data) >= 4 && bytes.Equal(data[:4], []byte{0x00, 0x00, 0x01, 0xb3}):
		return "mpg"
	default:
		return ""
	}
}

func isSupportedVideoName(lower string) bool {
	return strings.HasSuffix(lower, ".mp4") ||
		strings.HasSuffix(lower, ".mov") ||
		strings.HasSuffix(lower, ".m4v") ||
		strings.HasSuffix(lower, ".webm") ||
		strings.HasSuffix(lower, ".avi") ||
		strings.HasSuffix(lower, ".mpg") ||
		strings.HasSuffix(lower, ".mpeg") ||
		strings.HasSuffix(lower, ".3gp")
}

func isVideoThumbnailName(lower string) bool {
	return strings.Contains(lower, "thumb") && isSupportedImageName(lower)
}

func parsePositiveInt(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
