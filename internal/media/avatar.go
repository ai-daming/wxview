package media

import (
	"bytes"
	"context"
	"os"

	"wxview/internal/sqlitedb"
)

func ResolveAvatar(headImageDB string, username string, cacheDir string) Info {
	return ResolveAvatarContext(context.Background(), headImageDB, username, cacheDir)
}

func ResolveAvatarContext(ctx context.Context, headImageDB string, username string, cacheDir string) Info {
	if headImageDB == "" || username == "" {
		return notFound("avatar", "head_image cache or username is empty")
	}
	if _, err := os.Stat(headImageDB); err != nil {
		return notFound("avatar", "head_image cache not found")
	}
	db, err := sqlitedb.OpenReadOnly(ctx, headImageDB)
	if err != nil {
		return notFound("avatar", "local avatar not found in head_image.db")
	}
	defer db.Close()
	var data []byte
	err = db.QueryRowContext(context.Background(), "SELECT image_buffer FROM head_image WHERE username = ? AND length(image_buffer) > 0 LIMIT 1;", username).Scan(&data)
	if err != nil {
		return notFound("avatar", "local avatar not found in head_image.db")
	}
	if len(data) == 0 {
		return notFound("avatar", "local avatar image buffer is empty")
	}
	resolver := Resolver{CacheDir: cacheDir}
	ext := imageExt(data)
	path, err := resolver.writeDecodedToCache(headImageDB, data, ext, "avatar:"+username)
	if err != nil {
		return decryptFailed("avatar", headImageDB, err.Error(), false)
	}
	info := resolved("avatar", path, headImageDB, false, false)
	if ext != "gif" {
		info = withImageDimensions(info)
	}
	return info
}

func imageExt(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}):
		return "jpg"
	case bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G'}):
		return "png"
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return "gif"
	default:
		return "jpg"
	}
}
