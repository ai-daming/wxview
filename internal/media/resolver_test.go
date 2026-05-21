package media

import (
	"bytes"
	"crypto/aes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"wxview/internal/sqlitedb/sqlitetest"
)

func TestResolveImagePathMacOS4ExactThumbnail(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "wxid_owner_bcc2", "db_storage")
	chat := "53740852064@chatroom"
	imageDir := filepath.Join(dir, "wxid_owner_bcc2", "Message", "MessageTemp", md5Hex(chat), "Image")
	if err := os.MkdirAll(imageDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(imageDir, "361778758602_.pic_thumb.jpg")
	if err := os.WriteFile(want, []byte{0xff, 0xd8, 0xff, 0x00}, 0o600); err != nil {
		t.Fatal(err)
	}

	got, ok := resolveImagePath(dataDir, "", 1778758602, chat, 36)
	if !ok {
		t.Fatal("expected image path")
	}
	if got.path != want || !got.thumbnail {
		t.Fatalf("candidate = %+v, want %s thumbnail", got, want)
	}
}

func TestDecryptXORDatImage(t *testing.T) {
	dir := t.TempDir()
	raw := []byte{0xff, 0xd8, 0xff, 'j', 'p', 'g'}
	path := filepath.Join(dir, "a.dat")
	if err := os.WriteFile(path, xorBytes(raw, 0x55), 0o600); err != nil {
		t.Fatal(err)
	}

	decoded, ext, _, err := decryptImageBytes(path, imageKeyMaterial{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, raw) || ext != "jpg" {
		t.Fatalf("decoded=%x ext=%s", decoded, ext)
	}
}

func TestDecryptWechatV2DatImage(t *testing.T) {
	code := uint32(123456)
	wxid := "wxid_test_a0e4"
	aesKey := deriveV2AESKey(code, wxid)
	raw := []byte{0xff, 0xd8, 0xff, 'v', '2', '-', 'i', 'm', 'g'}
	padded := append([]byte{}, raw...)
	pad := aes.BlockSize - (len(padded) % aes.BlockSize)
	padded = append(padded, bytes.Repeat([]byte{byte(pad)}, pad)...)

	block, err := aes.NewCipher([]byte(aesKey))
	if err != nil {
		t.Fatal(err)
	}
	encrypted := append([]byte{}, padded...)
	for i := 0; i < len(encrypted); i += aes.BlockSize {
		block.Encrypt(encrypted[i:i+aes.BlockSize], encrypted[i:i+aes.BlockSize])
	}

	header := make([]byte, wechatV2HeaderSize)
	copy(header, wechatV2Magic)
	writeI32LEForTest(header, 6, len(raw))
	writeI32LEForTest(header, 10, 0)
	data := append(header, encrypted...)

	dir := t.TempDir()
	path := filepath.Join(dir, wxid, "Image", "a.dat")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	decoded, ext, _, err := decryptImageBytes(path, imageKeyMaterial{KVCommCodes: []uint32{code}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, raw) || ext != "jpg" {
		t.Fatalf("decoded=%x ext=%s", decoded, ext)
	}
}

func TestRejectsWeakBMPHeaderFalsePositive(t *testing.T) {
	data := []byte{
		0x42, 0x4d, 0xf7, 0xfb, 0x6e, 0x34, 0xf2, 0x64,
		0xa9, 0x63, 0xa8, 0x68, 0x3c, 0x82, 0x56, 0xcf,
		0x60, 0xe5, 0x5f, 0x89, 0xb7, 0x77, 0x94, 0x57,
		0x14, 0xfa, 0xcb, 0x4b, 0x65, 0x8c, 0x61, 0xe7,
	}
	if ext := detectImageExtension(data); ext != "" {
		t.Fatalf("weak BMP-like header detected as %s", ext)
	}
}

func TestWXIDCandidatesPreferAccountThenStrippedSuffix(t *testing.T) {
	got := collectWXIDCandidates("/tmp/xwechat_files/wxid_owner_bcc2/msg/attach/a.dat")
	want := []string{"wxid_owner_bcc2", "wxid_owner", "wxid"}
	for i, value := range want {
		if i >= len(got) || got[i] != value {
			t.Fatalf("wxid candidates = %#v, want prefix %#v", got, want)
		}
	}
}

func TestResolverAddsDirectImagePath(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "wxid_owner_bcc2", "db_storage")
	chat := "alice"
	imageDir := filepath.Join(dir, "wxid_owner_bcc2", "Message", "MessageTemp", md5Hex(chat), "Image")
	if err := os.MkdirAll(imageDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(imageDir, "71700000000_.pic_thumb.jpg")
	writeTestPNG(t, imagePath, 2, 3)

	resolver := NewResolver(dataDir, filepath.Join(dir, "cache"))
	info := resolver.ResolveImage(chat, 7, 1700000000, 3, "<msg><img /></msg>", false)
	if info.Status != "resolved" || info.Path != imagePath || info.Decoded || !info.Thumbnail {
		t.Fatalf("unexpected media info: %+v", info)
	}
	if info.Width != 2 || info.Height != 3 {
		t.Fatalf("dimensions = %dx%d, want 2x3", info.Width, info.Height)
	}
}

func TestResolveImageUsesDecodedCacheBeforeDecrypt(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "wxid_owner_bcc2", "db_storage")
	chat := "alice"
	imageDir := filepath.Join(dir, "wxid_owner_bcc2", "Message", "MessageTemp", md5Hex(chat), "Image")
	if err := os.MkdirAll(imageDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(imageDir, "71700000000.dat")
	raw := []byte{0xff, 0xd8, 0xff, 'j', 'p', 'g'}
	if err := os.WriteFile(imagePath, xorBytes(raw, 0x55), 0o600); err != nil {
		t.Fatal(err)
	}
	fixedTime := time.Unix(1700000000, 0)
	if err := os.Chtimes(imagePath, fixedTime, fixedTime); err != nil {
		t.Fatal(err)
	}

	resolver := NewResolver(dataDir, filepath.Join(dir, "cache"))
	first := resolver.ResolveImage(chat, 7, 1700000000, 3, "<msg><img /></msg>", false)
	if first.Status != "resolved" || first.Path == "" || !first.Decoded {
		t.Fatalf("unexpected first image info: %+v", first)
	}
	corruptFilePreservingTime(t, imagePath, fixedTime)
	second := resolver.ResolveImage(chat, 7, 1700000000, 3, "<msg><img /></msg>", false)
	if second.Status != "resolved" || second.Path != first.Path || !second.Decoded {
		t.Fatalf("decoded image cache was not used: first=%+v second=%+v", first, second)
	}
}

func TestResolverAddsDirectVideoPathAndThumbnail(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "wxid_owner_bcc2", "db_storage")
	chat := "alice"
	videoDir := filepath.Join(dir, "wxid_owner_bcc2", "Message", "MessageTemp", md5Hex(chat), "Video")
	if err := os.MkdirAll(videoDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	videoPath := filepath.Join(videoDir, "71700000000.mp4")
	writeTestMP4(t, videoPath)
	thumbPath := filepath.Join(videoDir, "71700000000_thumb.jpg")
	writeTestPNG(t, thumbPath, 4, 5)

	content := `<msg><videomsg md5="abc123def456" length="12" playlength="3" cdnthumbwidth="4" cdnthumbheight="5" /></msg>`
	resolver := NewResolver(dataDir, filepath.Join(dir, "cache"))
	info := resolver.ResolveVideo(chat, 7, 1700000000, 43, content, false)
	if info.Status != "resolved" || info.Path != videoPath || info.Decoded {
		t.Fatalf("unexpected video info: %+v", info)
	}
	if !info.Thumbnail || info.ThumbnailPath != thumbPath || info.Width != 4 || info.Height != 5 {
		t.Fatalf("unexpected video thumbnail: %+v", info)
	}
}

func TestResolveVideoUsesDecodedCacheBeforeDecrypt(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "wxid_owner_bcc2", "db_storage")
	chat := "alice"
	videoDir := filepath.Join(dir, "wxid_owner_bcc2", "Message", "MessageTemp", md5Hex(chat), "Video")
	if err := os.MkdirAll(videoDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	videoPath := filepath.Join(videoDir, "71700000000.dat")
	raw := testMP4Bytes()
	if err := os.WriteFile(videoPath, xorBytes(raw, 0x33), 0o600); err != nil {
		t.Fatal(err)
	}
	fixedTime := time.Unix(1700000000, 0)
	if err := os.Chtimes(videoPath, fixedTime, fixedTime); err != nil {
		t.Fatal(err)
	}

	resolver := NewResolver(dataDir, filepath.Join(dir, "cache"))
	first := resolver.ResolveVideo(chat, 7, 1700000000, 43, `<msg><videomsg length="24" /></msg>`, false)
	if first.Status != "resolved" || first.Path == "" || !first.Decoded {
		t.Fatalf("unexpected first video info: %+v", first)
	}
	corruptFilePreservingTime(t, videoPath, fixedTime)
	second := resolver.ResolveVideo(chat, 7, 1700000000, 43, `<msg><videomsg length="24" /></msg>`, false)
	if second.Status != "resolved" || second.Path != first.Path || !second.Decoded {
		t.Fatalf("decoded video cache was not used: first=%+v second=%+v", first, second)
	}
}

func TestResolverFindsVideoFromResourceDB(t *testing.T) {
	dir := t.TempDir()
	accountBase := filepath.Join(dir, "wxid_owner_bcc2")
	dataDir := filepath.Join(accountBase, "db_storage")
	videoDir := filepath.Join(accountBase, "msg", "video", "room-hash")
	if err := os.MkdirAll(videoDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	videoPath := filepath.Join(videoDir, "abc123def456.mp4")
	writeTestMP4(t, videoPath)
	resourceDB := filepath.Join(dir, "message_resource.db")
	sql := `
CREATE TABLE dir2id(username TEXT);
CREATE TABLE video_hardlink_info_v4(md5 TEXT, file_name TEXT, file_size INTEGER, modify_time INTEGER, dir1 INTEGER, dir2 INTEGER);
INSERT INTO dir2id(rowid, username) VALUES (1, 'room-hash');
INSERT INTO video_hardlink_info_v4(md5, file_name, file_size, modify_time, dir1, dir2) VALUES ('abc123def456', 'abc123def456.mp4', 12, 1700000000, 1, 0);
`
	createMediaTestDB(t, resourceDB, sql)

	content := `<msg><videomsg md5="abc123def456" length="12" /></msg>`
	resolver := NewResolver(dataDir, filepath.Join(dir, "cache"), resourceDB)
	info := resolver.ResolveVideo("alice", 7, 1700000000, 43, content, false)
	if info.Status != "resolved" || info.Path != videoPath {
		t.Fatalf("unexpected video info: %+v, want path %s", info, videoPath)
	}
}

func TestResolveVideoRejectsMTimeOnlyLegacyMatch(t *testing.T) {
	dir := t.TempDir()
	accountBase := filepath.Join(dir, "wxid_owner_bcc2")
	dataDir := filepath.Join(accountBase, "db_storage")
	videoDir := filepath.Join(accountBase, "msg", "video", "2026-05")
	if err := os.MkdirAll(videoDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	wrongVideoPath := filepath.Join(videoDir, "unrelated-video.mp4")
	writeTestMP4(t, wrongVideoPath)
	fixedTime := time.Unix(1700000100, 0)
	if err := os.Chtimes(wrongVideoPath, fixedTime, fixedTime); err != nil {
		t.Fatal(err)
	}

	content := `<msg><videomsg md5="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" newmd5="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" rawmd5="cccccccccccccccccccccccccccccccc" length="5783794" rawlength="26500350" /></msg>`
	resolver := NewResolver(dataDir, filepath.Join(dir, "cache"))
	info := resolver.ResolveVideo("room@chatroom", 3824, 1700000100, 43, content, false)
	if info.Status != "not_found" || info.Path != "" {
		t.Fatalf("mtime-only video candidate should not resolve: %+v", info)
	}
}

func TestResolveVideoPreservesThumbnailWhenMainMissing(t *testing.T) {
	dir := t.TempDir()
	accountBase := filepath.Join(dir, "wxid_owner_bcc2")
	dataDir := filepath.Join(accountBase, "db_storage")
	videoDir := filepath.Join(accountBase, "msg", "video", "2026-05")
	if err := os.MkdirAll(videoDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	thumbPath := filepath.Join(videoDir, "a6d6d819b222f42fe519c29c013e74db_thumb.jpg")
	if err := os.WriteFile(thumbPath, bytes.Repeat([]byte{1}, 18138), 0o600); err != nil {
		t.Fatal(err)
	}
	fixedTime := time.Unix(1700000100, 0)
	if err := os.Chtimes(thumbPath, fixedTime, fixedTime); err != nil {
		t.Fatal(err)
	}

	content := `<msg><videomsg md5="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" newmd5="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" rawmd5="cccccccccccccccccccccccccccccccc" length="5783794" rawlength="26500350" cdnthumblength="18138" cdnthumbwidth="304" cdnthumbheight="540" /></msg>`
	resolver := NewResolver(dataDir, filepath.Join(dir, "cache"))
	info := resolver.ResolveVideo("room@chatroom", 3824, 1700000100, 43, content, false)
	if info.Status != "not_found" || info.Path != "" || !info.Thumbnail || info.ThumbnailPath != thumbPath {
		t.Fatalf("expected missing video with resolved thumbnail: %+v", info)
	}
	if info.Width != 304 || info.Height != 540 {
		t.Fatalf("thumbnail dimensions = %dx%d, want 304x540", info.Width, info.Height)
	}
}

func TestResolveFilePathMacOS4(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "wxid_owner_bcc2", "db_storage")
	chat := "53740852064@chatroom"
	fileDir := filepath.Join(dir, "wxid_owner_bcc2", "Message", "MessageTemp", md5Hex(chat), "File")
	if err := os.MkdirAll(fileDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(fileDir, "report.pdf")
	if err := os.WriteFile(filePath, []byte("pdf"), 0o600); err != nil {
		t.Fatal(err)
	}
	content := `<msg><appmsg><type>6</type><title>report.pdf</title><appattach><fileext>pdf</fileext><totallen>3</totallen></appattach></appmsg></msg>`
	resolver := NewResolver(dataDir, filepath.Join(dir, "cache"))
	info := resolver.ResolveFile(chat, 7, 1700000000, int64(6)<<32|49, content, false)
	if info.Status != "resolved" || info.Path != filePath {
		t.Fatalf("unexpected file info: %+v, want path %s", info, filePath)
	}
}

func TestResolveVoiceFromMediaDB(t *testing.T) {
	stubVoiceWAVEncoder(t)
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "wxid_owner_bcc2", "db_storage")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	mediaDB := filepath.Join(dir, "media_0.db")
	sql := `
CREATE TABLE Name2Id(user_name TEXT);
INSERT INTO Name2Id(rowid, user_name) VALUES (9, 'alice');
CREATE TABLE VoiceInfo(chat_name_id INTEGER, local_id INTEGER, svr_id INTEGER, voice_data BLOB);
INSERT INTO VoiceInfo(chat_name_id, local_id, svr_id, voice_data) VALUES (9, 7, 0, X'02232153494C4B5F5633616263');
`
	createMediaTestDB(t, mediaDB, sql)
	resolver := NewResolver(dataDir, filepath.Join(dir, "cache"), mediaDB)
	info := resolver.ResolveVoice("alice", 7, 0, 34)
	if info.Status != "resolved" || info.Path == "" {
		t.Fatalf("unexpected voice info: %+v", info)
	}
	data, err := os.ReadFile(info.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("RIFF")) || filepath.Ext(info.Path) != ".wav" || !info.Decoded {
		t.Fatalf("voice was not exported as decoded wav: info=%+v data=%x", info, data)
	}
}

func TestResolveVoiceUsesCachedWAVBeforeTranscoding(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "wxid_owner_bcc2", "db_storage")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	mediaDB := filepath.Join(dir, "media_0.db")
	sql := `
CREATE TABLE Name2Id(user_name TEXT);
INSERT INTO Name2Id(rowid, user_name) VALUES (9, 'alice');
CREATE TABLE VoiceInfo(chat_name_id INTEGER, local_id INTEGER, svr_id INTEGER, voice_data BLOB);
INSERT INTO VoiceInfo(chat_name_id, local_id, svr_id, voice_data) VALUES (9, 7, 0, X'02232153494C4B5F5633616263');
`
	createMediaTestDB(t, mediaDB, sql)

	old := voiceWAVEncoder
	calls := 0
	voiceWAVEncoder = func(data []byte) ([]byte, error) {
		calls++
		return append([]byte("RIFF"), data...), nil
	}
	t.Cleanup(func() {
		voiceWAVEncoder = old
	})

	resolver := NewResolver(dataDir, filepath.Join(dir, "cache"), mediaDB)
	first := resolver.ResolveVoice("alice", 7, 0, 34)
	second := resolver.ResolveVoice("alice", 7, 0, 34)
	if first.Status != "resolved" || second.Status != "resolved" || first.Path == "" || first.Path != second.Path {
		t.Fatalf("unexpected cached voice results: first=%+v second=%+v", first, second)
	}
	if calls != 1 {
		t.Fatalf("voice encoder calls = %d, want 1", calls)
	}
}

func TestResolveVoicesBatchFromMediaDB(t *testing.T) {
	stubVoiceWAVEncoder(t)
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "wxid_owner_bcc2", "db_storage")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	mediaDB := filepath.Join(dir, "media_0.db")
	sql := `
CREATE TABLE Name2Id(user_name TEXT);
INSERT INTO Name2Id(rowid, user_name) VALUES (9, 'alice');
CREATE TABLE VoiceInfo(chat_name_id INTEGER, local_id INTEGER, svr_id INTEGER, voice_data BLOB);
INSERT INTO VoiceInfo(chat_name_id, local_id, svr_id, voice_data) VALUES (9, 7, 0, X'02232153494C4B5F5633616263');
INSERT INTO VoiceInfo(chat_name_id, local_id, svr_id, voice_data) VALUES (9, 8, 123, X'02232153494C4B5F5633646566');
`
	createMediaTestDB(t, mediaDB, sql)
	resolver := NewResolver(dataDir, filepath.Join(dir, "cache"), mediaDB)
	got := resolver.ResolveVoices([]VoiceRequest{
		{Key: "local", ChatUsername: "alice", LocalID: 7, RawType: 34},
		{Key: "server", ChatUsername: "alice", LocalID: 8, ServerID: 123, RawType: 34},
	})
	if got["local"].Status != "resolved" || got["server"].Status != "resolved" {
		t.Fatalf("batch voice results = %+v, want both resolved", got)
	}
	if filepath.Ext(got["local"].Path) != ".wav" || filepath.Ext(got["server"].Path) != ".wav" {
		t.Fatalf("batch voice paths = %+v, want wav files", got)
	}
}

func TestNormalizeSilkPayloadStripsWechatPrefix(t *testing.T) {
	got, err := normalizeSilkPayload([]byte{0x00, 0xff, 0x02, '#', '!', 'S', 'I', 'L', 'K', '_', 'V', '3'})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("#!SILK_V3")) {
		t.Fatalf("normalized payload = %q, want silk header", got)
	}
}

func stubVoiceWAVEncoder(t *testing.T) {
	t.Helper()
	old := voiceWAVEncoder
	voiceWAVEncoder = func(data []byte) ([]byte, error) {
		normalized, err := normalizeSilkPayload(data)
		if err != nil {
			return nil, err
		}
		if !bytes.HasPrefix(normalized, []byte("#!SILK_V3")) {
			t.Fatalf("voice encoder received non-silk payload: %x", data)
		}
		return append([]byte("RIFF"), normalized...), nil
	}
	t.Cleanup(func() {
		voiceWAVEncoder = old
	})
}

func TestResolveAvatarFromHeadImageDB(t *testing.T) {
	dir := t.TempDir()
	headDB := filepath.Join(dir, "head_image.db")
	sql := `
CREATE TABLE head_image(username TEXT, image_buffer BLOB);
INSERT INTO head_image(username, image_buffer) VALUES ('alice', X'FFD8FF00');
`
	createMediaTestDB(t, headDB, sql)
	info := ResolveAvatar(headDB, "alice", filepath.Join(dir, "cache"))
	if info.Status != "resolved" || info.Path == "" {
		t.Fatalf("unexpected avatar info: %+v", info)
	}
	if _, err := os.Stat(info.Path); err != nil {
		t.Fatal(err)
	}
}

func TestDecryptXORDatVideo(t *testing.T) {
	dir := t.TempDir()
	raw := testMP4Bytes()
	path := filepath.Join(dir, "a.dat")
	if err := os.WriteFile(path, xorBytes(raw, 0x33), 0o600); err != nil {
		t.Fatal(err)
	}

	decoded, ext, _, err := decryptVideoBytes(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, raw) || ext != "mp4" {
		t.Fatalf("decoded=%x ext=%s", decoded, ext)
	}
}

func writeI32LEForTest(data []byte, offset int, value int) {
	data[offset] = byte(value)
	data[offset+1] = byte(value >> 8)
	data[offset+2] = byte(value >> 16)
	data[offset+3] = byte(value >> 24)
}

func writeTestPNG(t *testing.T, path string, width int, height int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: 0x44, G: 0x88, B: 0xcc, A: 0xff})
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
}

func writeTestMP4(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, testMP4Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func corruptFilePreservingTime(t *testing.T, path string, modTime time.Time) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte{0}, int(info.Size()))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
}

func testMP4Bytes() []byte {
	data := []byte{0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}
	return append(data, []byte(fmt.Sprintf("%012d", 1))...)
}

func createMediaTestDB(t *testing.T, path string, script string) {
	t.Helper()
	db := sqlitetest.CreateDB(t, path, script)
	db.Close()
}
