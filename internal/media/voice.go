package media

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/klauspost/compress/zstd"

	"wxview/internal/app"
	"wxview/internal/sqlitedb"
)

var voiceWAVEncoder = silkToWAV

type VoiceRequest struct {
	Key          string
	ChatUsername string
	LocalID      int64
	ServerID     int64
	RawType      int64
}

func (r Resolver) ResolveVoice(chatUsername string, localID int64, serverID int64, rawType int64) Info {
	if int64(uint64(rawType)&0xffffffff) != 34 {
		return notFound("voice", "not a voice message")
	}
	for _, dbPath := range r.ResourceDBs {
		sqlitedb.ObserveRead(r.context(), dbPath)
		if _, err := os.Stat(dbPath); err != nil {
			continue
		}
		if chatUsername != "" && localID > 0 {
			if chatID, ok := queryChatNameID(r.context(), dbPath, chatUsername); ok {
				if serverID > 0 {
					if data, ok := queryVoiceByChatServerID(r.context(), dbPath, chatID, serverID); ok {
						return r.writeVoiceData(dbPath, data, fmt.Sprintf("svr:%d", serverID))
					}
				}
				if data, ok := queryVoiceByLocalID(r.context(), dbPath, chatID, localID); ok {
					return r.writeVoiceData(dbPath, data, fmt.Sprintf("chat:%d:%d", chatID, localID))
				}
			}
		}
	}
	return notFound("voice", "voice_data not found in media_*.db")
}

func (r Resolver) ResolveVoices(requests []VoiceRequest) map[string]Info {
	out := make(map[string]Info, len(requests))
	pending := make(map[string]VoiceRequest, len(requests))
	for _, req := range requests {
		if req.Key == "" {
			continue
		}
		if int64(uint64(req.RawType)&0xffffffff) != 34 {
			out[req.Key] = notFound("voice", "not a voice message")
			continue
		}
		pending[req.Key] = req
	}
	for _, dbPath := range r.ResourceDBs {
		if len(pending) == 0 {
			break
		}
		sqlitedb.ObserveRead(r.context(), dbPath)
		if _, err := os.Stat(dbPath); err != nil {
			continue
		}
		db, err := sqlitedb.OpenReadOnly(r.context(), dbPath)
		if err != nil {
			continue
		}
		r.resolveVoicesFromDB(db, dbPath, pending, out)
		_ = db.Close()
	}
	for key := range pending {
		out[key] = notFound("voice", "voice_data not found in media_*.db")
	}
	return out
}

func (r Resolver) resolveVoicesFromDB(db *sql.DB, dbPath string, pending map[string]VoiceRequest, out map[string]Info) {
	hasVoiceInfo, err := sqlitedb.TableExists(context.Background(), db, "VoiceInfo")
	if err != nil || !hasVoiceInfo {
		return
	}
	chatIDs := queryChatNameIDs(db, pending)
	if len(chatIDs) == 0 {
		return
	}
	r.resolveVoicesByServerID(db, dbPath, chatIDs, pending, out)
	if len(pending) == 0 {
		return
	}
	r.resolveVoicesByLocalID(db, dbPath, chatIDs, pending, out)
}

func (r Resolver) resolveVoicesByServerID(db *sql.DB, dbPath string, chatIDs map[string]int64, pending map[string]VoiceRequest, out map[string]Info) {
	keysByChatServer := map[string][]string{}
	serverIDsByChat := map[int64][]int64{}
	seen := map[string]bool{}
	for key, req := range pending {
		if req.ServerID <= 0 {
			continue
		}
		chatID := chatIDs[req.ChatUsername]
		if chatID <= 0 {
			continue
		}
		pair := voicePairKey(chatID, req.ServerID)
		keysByChatServer[pair] = append(keysByChatServer[pair], key)
		if !seen[pair] {
			seen[pair] = true
			serverIDsByChat[chatID] = append(serverIDsByChat[chatID], req.ServerID)
		}
	}
	for chatID, serverIDs := range serverIDsByChat {
		rows, err := db.QueryContext(context.Background(),
			"SELECT svr_id, voice_data FROM VoiceInfo WHERE chat_name_id = ? AND svr_id IN ("+int64Placeholders(len(serverIDs))+") AND length(voice_data) > 0;",
			append([]any{chatID}, int64Args(serverIDs)...)...,
		)
		if err != nil {
			continue
		}
		for rows.Next() {
			var serverID int64
			var data []byte
			if err := rows.Scan(&serverID, &data); err != nil || len(data) == 0 {
				continue
			}
			for _, key := range keysByChatServer[voicePairKey(chatID, serverID)] {
				if _, done := out[key]; done {
					continue
				}
				out[key] = r.writeVoiceData(dbPath, data, fmt.Sprintf("svr:%d", serverID))
				delete(pending, key)
			}
		}
		_ = rows.Close()
	}
}

func (r Resolver) resolveVoicesByLocalID(db *sql.DB, dbPath string, chatIDs map[string]int64, pending map[string]VoiceRequest, out map[string]Info) {
	pairToKeys := map[string][]string{}
	chatIDSet := map[int64]bool{}
	localIDSet := map[int64]bool{}
	var chatIDList []int64
	var localIDList []int64
	for key, req := range pending {
		if req.ChatUsername == "" || req.LocalID <= 0 {
			continue
		}
		chatID := chatIDs[req.ChatUsername]
		if chatID <= 0 {
			continue
		}
		pair := voicePairKey(chatID, req.LocalID)
		pairToKeys[pair] = append(pairToKeys[pair], key)
		if !chatIDSet[chatID] {
			chatIDSet[chatID] = true
			chatIDList = append(chatIDList, chatID)
		}
		if !localIDSet[req.LocalID] {
			localIDSet[req.LocalID] = true
			localIDList = append(localIDList, req.LocalID)
		}
	}
	if len(chatIDList) == 0 || len(localIDList) == 0 {
		return
	}
	args := append(int64Args(chatIDList), int64Args(localIDList)...)
	rows, err := db.QueryContext(context.Background(),
		"SELECT chat_name_id, local_id, voice_data FROM VoiceInfo WHERE chat_name_id IN ("+int64Placeholders(len(chatIDList))+") AND local_id IN ("+int64Placeholders(len(localIDList))+") AND length(voice_data) > 0;",
		args...,
	)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var chatID int64
		var localID int64
		var data []byte
		if err := rows.Scan(&chatID, &localID, &data); err != nil || len(data) == 0 {
			continue
		}
		for _, key := range pairToKeys[voicePairKey(chatID, localID)] {
			if _, done := out[key]; done {
				continue
			}
			out[key] = r.writeVoiceData(dbPath, data, fmt.Sprintf("chat:%d:%d", chatID, localID))
			delete(pending, key)
		}
	}
}

func queryChatNameIDs(db *sql.DB, pending map[string]VoiceRequest) map[string]int64 {
	hasName2ID, err := sqlitedb.TableExists(context.Background(), db, "Name2Id")
	if err != nil || !hasName2ID {
		return nil
	}
	var names []string
	seen := map[string]bool{}
	for _, req := range pending {
		if req.ChatUsername == "" || seen[req.ChatUsername] {
			continue
		}
		seen[req.ChatUsername] = true
		names = append(names, req.ChatUsername)
	}
	if len(names) == 0 {
		return nil
	}
	args := make([]any, 0, len(names))
	for _, name := range names {
		args = append(args, name)
	}
	rows, err := db.QueryContext(context.Background(),
		"SELECT rowid, user_name FROM Name2Id WHERE user_name IN ("+strings.TrimRight(strings.Repeat("?,", len(names)), ",")+");",
		args...,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var id int64
		var username string
		if err := rows.Scan(&id, &username); err == nil && id > 0 {
			out[username] = id
		}
	}
	return out
}

func voicePairKey(chatID int64, localID int64) string {
	return strconv.FormatInt(chatID, 10) + ":" + strconv.FormatInt(localID, 10)
}

func int64Placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("?,", n), ",")
}

func int64Args(values []int64) []any {
	args := make([]any, 0, len(values))
	for _, value := range values {
		args = append(args, value)
	}
	return args
}

func (r Resolver) writeVoiceData(sourcePath string, data []byte, _ string) Info {
	if len(data) == 0 {
		return notFound("voice", "empty voice_data")
	}
	path, err := r.voiceWAVCachePath(data)
	if err != nil {
		return decryptFailed("voice", sourcePath, err.Error(), false)
	}
	if fileExists(path) {
		return resolvedVoiceInfo(path, sourcePath)
	}
	wav, err := voiceWAVEncoder(data)
	if err != nil {
		return decryptFailed("voice", sourcePath, "voice_data wav conversion failed: "+err.Error(), false)
	}
	if err := r.writeBytesToCache(path, wav); err != nil {
		return decryptFailed("voice", sourcePath, err.Error(), false)
	}
	return resolvedVoiceInfo(path, sourcePath)
}

func (r Resolver) voiceWAVCachePath(data []byte) (string, error) {
	if err := os.MkdirAll(r.CacheDir, 0o700); err != nil {
		return "", err
	}
	if err := app.ChownForSudo(r.CacheDir); err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return filepath.Join(r.CacheDir, hex.EncodeToString(digest[:])[:24]+".wav"), nil
}

func resolvedVoiceInfo(path string, sourcePath string) Info {
	return Info{
		Kind:       "voice",
		Status:     "resolved",
		Path:       path,
		SourcePath: sourcePath,
		Decoded:    true,
		Thumbnail:  false,
		Reason:     "voice_data decoded to wav",
	}
}

var (
	silkMagic = []byte("#!SILK")
	zstdMagic = []byte{0x28, 0xb5, 0x2f, 0xfd}
)

func normalizeSilkPayload(data []byte) ([]byte, error) {
	current := data
	for range 3 {
		trimmed := bytes.TrimLeft(current, "\x00\xff")
		if idx := bytes.Index(trimmed, silkMagic); idx >= 0 {
			return trimmed[idx:], nil
		}
		if next, ok := decompressVoicePayload(trimmed); ok {
			current = next
			continue
		}
		if !bytes.Equal(trimmed, current) {
			current = trimmed
			continue
		}
		break
	}
	return nil, fmt.Errorf("silk header missing")
}

func decompressVoicePayload(data []byte) ([]byte, bool) {
	if len(data) >= 4 && bytes.Equal(data[:4], zstdMagic) {
		reader, err := zstd.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, false
		}
		defer reader.Close()
		out, err := io.ReadAll(reader)
		return out, err == nil
	}
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, false
		}
		defer reader.Close()
		out, err := io.ReadAll(reader)
		return out, err == nil
	}
	if len(data) >= 2 && data[0] == 0x78 {
		reader, err := zlib.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, false
		}
		defer reader.Close()
		out, err := io.ReadAll(reader)
		return out, err == nil
	}
	return nil, false
}

func queryVoiceByChatServerID(ctx context.Context, dbPath string, chatNameID int64, serverID int64) ([]byte, bool) {
	return queryVoiceData(ctx, dbPath, "SELECT voice_data FROM VoiceInfo WHERE chat_name_id = ? AND svr_id = ? AND length(voice_data) > 0 LIMIT 1;", chatNameID, serverID)
}

func queryVoiceByLocalID(ctx context.Context, dbPath string, chatNameID int64, localID int64) ([]byte, bool) {
	return queryVoiceData(ctx, dbPath, "SELECT voice_data FROM VoiceInfo WHERE chat_name_id = ? AND local_id = ? AND length(voice_data) > 0 LIMIT 1;", chatNameID, localID)
}

func queryVoiceData(ctx context.Context, dbPath string, query string, args ...any) ([]byte, bool) {
	db, err := sqlitedb.OpenReadOnly(ctx, dbPath)
	if err != nil {
		return nil, false
	}
	defer db.Close()
	var data []byte
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&data); err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
}

func queryChatNameID(ctx context.Context, dbPath string, username string) (int64, bool) {
	db, err := sqlitedb.OpenReadOnly(ctx, dbPath)
	if err != nil {
		return 0, false
	}
	defer db.Close()
	var id int64
	if err := db.QueryRowContext(context.Background(), "SELECT rowid FROM Name2Id WHERE user_name = ? LIMIT 1;", username).Scan(&id); err != nil {
		return 0, false
	}
	return id, id > 0
}
