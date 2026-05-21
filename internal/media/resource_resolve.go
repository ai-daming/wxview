package media

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ResourceRequest struct {
	Kind       string
	Keys       []string
	Name       string
	Size       int64
	CreateTime int64
	LocalID    int64
	Width      int
	Height     int
}

func (r Resolver) ResolveResource(req ResourceRequest) Info {
	kind := strings.TrimSpace(req.Kind)
	mediaType := resourceMediaType(kind)
	if mediaType == "" {
		return Info{}
	}
	keys := cleanMediaKeys(req.Keys)
	if len(keys) == 0 && strings.TrimSpace(req.Name) == "" && req.Size <= 0 {
		return notFound(kind, "media locator missing")
	}
	files := queryResourceMedia(r.ResourceDBs, r.DataDir, mediaType, resourceKeysForLookup(keys, req.Name))
	if len(files) == 0 {
		return notFound(kind, "local media resource not found")
	}
	switch mediaType {
	case "image":
		if found, ok := chooseResourceImage(files, req); ok {
			return r.resourceImageInfo(kind, found)
		}
	case "video":
		hints := videoHints{
			keys:        keys,
			sizes:       map[int64]bool{},
			thumbWidth:  req.Width,
			thumbHeight: req.Height,
		}
		if req.Size > 0 {
			hints.sizes[req.Size] = true
		}
		if found, ok := chooseVideoCandidate(files, hints, req.CreateTime, req.LocalID); ok {
			return r.resourceVideoInfo(kind, found, req)
		}
	case "file":
		hints := fileHints{
			title: strings.TrimSpace(req.Name),
			size:  req.Size,
			keys:  keys,
		}
		if ext := strings.TrimPrefix(filepath.Ext(req.Name), "."); ext != "" {
			hints.ext = ext
		}
		if found, ok := chooseFileCandidate(files, hints, req.CreateTime, req.LocalID); ok {
			return resolved(kind, found.path, found.path, false, false)
		}
	}
	return notFound(kind, "local media resource not found")
}

func resourceMediaType(kind string) string {
	switch kind {
	case "image", "cover", "emoji", "sticker":
		return "image"
	case "video":
		return "video"
	case "file":
		return "file"
	default:
		return ""
	}
}

func resourceKeysForLookup(keys []string, name string) []string {
	out := append([]string{}, keys...)
	name = strings.TrimSpace(name)
	if name != "" {
		out = append(out, name)
	}
	return out
}

func chooseResourceImage(files []resourceMediaFile, req ResourceRequest) (resourceMediaFile, bool) {
	type scoredResourceFile struct {
		file  resourceMediaFile
		score int
	}
	var candidates []scoredResourceFile
	for _, file := range files {
		if file.Path == "" || !fileExists(file.Path) {
			continue
		}
		lower := strings.ToLower(filepath.Base(file.Path))
		if !isSupportedImageName(lower) && !strings.HasSuffix(lower, ".dat") {
			continue
		}
		score := scoreResourceFile(file, req)
		if score <= 0 {
			continue
		}
		candidates = append(candidates, scoredResourceFile{file: file, score: score})
	}
	if len(candidates) == 0 {
		return resourceMediaFile{}, false
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		if candidates[i].file.ModifyTime != candidates[j].file.ModifyTime {
			return candidates[i].file.ModifyTime > candidates[j].file.ModifyTime
		}
		return candidates[i].file.Path < candidates[j].file.Path
	})
	return candidates[0].file, true
}

func scoreResourceFile(file resourceMediaFile, req ResourceRequest) int {
	name := strings.ToLower(filepath.Base(file.Path))
	wantName := strings.ToLower(strings.TrimSpace(req.Name))
	score := 0
	for _, key := range cleanMediaKeys(req.Keys) {
		if key != "" && (strings.Contains(name, key) || strings.EqualFold(file.Key, key)) {
			score += 200
		}
	}
	if wantName != "" {
		switch {
		case strings.EqualFold(filepath.Base(file.Path), wantName):
			score += 160
		case strings.Contains(name, wantName):
			score += 80
		}
	}
	if req.Size > 0 && file.Size == req.Size {
		score += 80
	}
	if req.CreateTime > 0 && file.ModifyTime > 0 && absInt64(file.ModifyTime-req.CreateTime) <= 7*24*3600 {
		score += 20
	}
	return score
}

func (r Resolver) resourceImageInfo(kind string, file resourceMediaFile) Info {
	path := file.Path
	sourcePath := file.Path
	decoded := false
	if strings.HasSuffix(strings.ToLower(path), ".dat") {
		if out, ok := r.lookupDecodedCache(path); ok {
			path = out
			decoded = true
		} else {
			out, err := r.decryptImageToCache(path)
			if err != nil {
				return decryptFailed(kind, sourcePath, err.Error(), isThumbnailPath(sourcePath))
			}
			path = out
			decoded = true
		}
	}
	return withImageDimensions(resolved(kind, path, sourcePath, decoded, isThumbnailPath(sourcePath)))
}

func (r Resolver) resourceVideoInfo(kind string, found videoCandidate, req ResourceRequest) Info {
	if _, err := os.Stat(found.path); err != nil {
		return notFound(kind, "local media resource not found")
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
				info := decryptFailed(kind, sourcePath, err.Error(), false)
				r.attachVideoThumbnail(&info, found)
				return info
			}
			path = out
			decoded = true
		}
	}
	info := resolved(kind, path, sourcePath, decoded, found.thumbnailPath != "")
	r.attachVideoThumbnail(&info, found)
	if info.Width == 0 && req.Width > 0 {
		info.Width = req.Width
	}
	if info.Height == 0 && req.Height > 0 {
		info.Height = req.Height
	}
	return info
}
