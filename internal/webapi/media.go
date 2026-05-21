package webapi

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"wxview/internal/key"
	"wxview/internal/messages"
)

func (h *handler) handleMedia(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or HEAD")
		return
	}
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		writeError(w, http.StatusBadRequest, "missing_path", "path is required")
		return
	}
	resolved, err := h.resolveMediaPath(path)
	if err != nil {
		writeMediaError(w, err)
		return
	}
	file, err := os.Open(resolved)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "media file not found")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		writeError(w, http.StatusNotFound, "not_found", "media file not found")
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

func (h *handler) attachMediaURLs(list []messages.Message) {
	for i := range list {
		detail := list[i].ContentDetail
		if len(detail) == 0 {
			continue
		}
		if path := strings.TrimSpace(detail["path"]); path != "" {
			detail["media_url"] = mediaURL(path)
		}
		if path := strings.TrimSpace(detail["thumbnail_path"]); path != "" {
			detail["thumbnail_url"] = mediaURL(path)
		}
	}
}

func mediaURL(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	values := url.Values{}
	values.Set("path", path)
	return "/v1/media?" + values.Encode()
}

func (h *handler) resolveMediaPath(raw string) (string, error) {
	cleanPath, err := cleanMediaPath(raw)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(cleanPath)
	if err != nil || info.IsDir() {
		return "", mediaNotFoundError()
	}
	roots, err := h.mediaRoots()
	if err != nil {
		return "", err
	}
	if !pathWithinAnyRoot(cleanPath, roots) {
		return "", mediaForbiddenError()
	}
	return cleanPath, nil
}

func (h *handler) mediaRoots() ([]string, error) {
	target, err := h.discoverContact()
	if err != nil {
		return nil, err
	}
	cachePath, err := h.contactCachePath(target.Account)
	if err != nil {
		return nil, err
	}
	return allowedMediaRoots(target, cachePath), nil
}

func allowedMediaRoots(target key.TargetDB, cachePath string) []string {
	var roots []string
	if strings.TrimSpace(target.DataDir) != "" {
		roots = append(roots, target.DataDir)
		parent := filepath.Dir(target.DataDir)
		if parent != "." && parent != target.DataDir {
			roots = append(roots, parent)
		}
	}
	if strings.TrimSpace(cachePath) != "" {
		cacheRoot := filepath.Dir(filepath.Dir(cachePath))
		if cacheRoot != "." && cacheRoot != string(filepath.Separator) {
			roots = append(roots, cacheRoot)
		}
	}
	return roots
}

func cleanMediaPath(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("path is required")
	}
	cleaned := filepath.Clean(raw)
	if !filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("path must be absolute")
	}
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}
	return abs, nil
}

func pathWithinAnyRoot(path string, roots []string) bool {
	path = cleanExistingPathForCompare(path)
	for _, root := range roots {
		root = cleanExistingPathForCompare(root)
		if root == "" {
			continue
		}
		if path == root {
			return true
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			continue
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return true
		}
	}
	return false
}

func cleanExistingPathForCompare(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

type mediaAccessError struct {
	status  int
	code    string
	message string
}

func (e mediaAccessError) Error() string {
	return e.message
}

func mediaNotFoundError() error {
	return mediaAccessError{status: http.StatusNotFound, code: "not_found", message: "media file not found"}
}

func mediaForbiddenError() error {
	return mediaAccessError{status: http.StatusForbidden, code: "forbidden", message: "media path is outside the current wxview account cache or WeChat data directory"}
}

func writeMediaError(w http.ResponseWriter, err error) {
	if access, ok := err.(mediaAccessError); ok {
		writeError(w, access.status, access.code, access.message)
		return
	}
	writeError(w, http.StatusBadRequest, "bad_request", err.Error())
}
