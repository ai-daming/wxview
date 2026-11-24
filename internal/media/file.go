package media

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type fileEnvelope struct {
	App fileAppMsg `xml:"appmsg"`
}

type fileAppMsg struct {
	Title     string         `xml:"title"`
	MD5       string         `xml:"md5"`
	AppAttach *fileAppAttach `xml:"appattach"`
}

type fileAppAttach struct {
	FileExt  string `xml:"fileext"`
	TotalLen string `xml:"totallen"`
}

type fileHints struct {
	title string
	md5   string
	ext   string
	size  int64
	keys  []string
}

type fileCandidate struct {
	path       string
	score      int
	modifyTime int64
	size       int64
}

func (r Resolver) ResolveFile(chatUsername string, localID int64, createTime int64, rawType int64, content string, isChatroom bool) Info {
	msgType := int64(uint64(rawType) & 0xffffffff)
	subType := int64(uint64(rawType) >> 32)
	if msgType != 49 || subType != 6 {
		return notFound("file", "not a file appmsg")
	}
	hints := parseFileHints(messageBody(content, isChatroom))
	if found, ok := r.resolveFilePath(chatUsername, createTime, localID, hints); ok {
		return resolved("file", found.path, found.path, false, false)
	}
	return notFound("file", "local file not found")
}

func (r Resolver) resolveFilePath(chatUsername string, createTime int64, localID int64, hints fileHints) (fileCandidate, bool) {
	if len(hints.keys) > 0 {
		files := queryResourceMedia(r.context(), r.ResourceDBs, r.DataDir, "file", hints.keys)
		if found, ok := chooseFileCandidate(files, hints, createTime, localID); ok {
			return found, true
		}
	}
	var files []resourceMediaFile
	for _, base := range mediaBases(r.DataDir) {
		files = append(files, scanFileCandidateDirs(base, chatUsername, createTime)...)
	}
	return chooseFileCandidate(files, hints, createTime, localID)
}

func scanFileCandidateDirs(wechatBase string, chatUsername string, createTime int64) []resourceMediaFile {
	var dirs []string
	if chatUsername != "" {
		dirs = append(dirs, filepath.Join(wechatBase, "Message", "MessageTemp", md5Hex(chatUsername), "File"))
	}
	month := ""
	if createTime > 0 {
		month = time.Unix(createTime, 0).Format("2006-01")
		dirs = append(dirs, filepath.Join(wechatBase, "msg", "file", month))
	}
	fileRoot := filepath.Join(wechatBase, "msg", "file")
	if entries, err := os.ReadDir(fileRoot); err == nil {
		for _, entry := range entries {
			if entry.IsDir() && (month == "" || entry.Name() != month) {
				dirs = append(dirs, filepath.Join(fileRoot, entry.Name()))
			}
		}
	}

	var files []resourceMediaFile
	seen := map[string]bool{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			if seen[path] {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			seen[path] = true
			files = append(files, resourceMediaFile{
				Name:       entry.Name(),
				Size:       info.Size(),
				ModifyTime: info.ModTime().Unix(),
				Path:       path,
			})
		}
	}
	return files
}

func chooseFileCandidate(files []resourceMediaFile, hints fileHints, createTime int64, localID int64) (fileCandidate, bool) {
	var candidates []fileCandidate
	for _, file := range files {
		if file.Path == "" || !fileExists(file.Path) {
			continue
		}
		score := scoreFileCandidate(file, hints, createTime, localID)
		if score <= 0 {
			continue
		}
		candidates = append(candidates, fileCandidate{path: file.Path, score: score, modifyTime: file.ModifyTime, size: file.Size})
	}
	if len(candidates) == 0 {
		return fileCandidate{}, false
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		if candidates[i].modifyTime != candidates[j].modifyTime {
			return candidates[i].modifyTime > candidates[j].modifyTime
		}
		return candidates[i].path < candidates[j].path
	})
	return candidates[0], true
}

func scoreFileCandidate(file resourceMediaFile, hints fileHints, createTime int64, localID int64) int {
	name := strings.ToLower(file.Name)
	title := strings.ToLower(strings.TrimSpace(hints.title))
	score := 0
	if title != "" {
		switch {
		case strings.EqualFold(file.Name, hints.title):
			score += 500
		case strings.Contains(name, title):
			score += 240
		}
	}
	if hints.md5 != "" && (strings.Contains(name, strings.ToLower(hints.md5)) || strings.EqualFold(file.Key, hints.md5)) {
		score += 300
	}
	if hints.ext != "" && strings.EqualFold(filepath.Ext(file.Name), "."+strings.TrimPrefix(hints.ext, ".")) {
		score += 40
	}
	if hints.size > 0 && file.Size == hints.size {
		score += 120
	}
	if createTime > 0 && file.ModifyTime > 0 {
		diff := absInt64(file.ModifyTime - createTime)
		if diff <= int64(7*24*time.Hour/time.Second) {
			score += 80
		}
	}
	if localID > 0 && strings.Contains(name, strconv.FormatInt(localID, 10)) {
		score += 80
	}
	return score
}

func parseFileHints(content string) fileHints {
	var env fileEnvelope
	_ = xml.Unmarshal([]byte(extractXMLContent(content)), &env)
	hints := fileHints{
		title: strings.TrimSpace(env.App.Title),
		md5:   strings.ToLower(strings.TrimSpace(env.App.MD5)),
	}
	if env.App.AppAttach != nil {
		hints.ext = strings.TrimSpace(env.App.AppAttach.FileExt)
		hints.size = parsePositiveInt(env.App.AppAttach.TotalLen)
	}
	hints.keys = cleanMediaKeys([]string{hints.md5})
	return hints
}
