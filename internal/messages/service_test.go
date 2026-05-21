package messages

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	zstdpkg "github.com/klauspost/compress/zstd"

	"wxview/internal/media"
	"wxview/internal/sqlitedb"
	"wxview/internal/sqlitedb/sqlitetest"
)

func TestListMergesShardsSortsAscendingAndPaginates(t *testing.T) {
	dir := t.TempDir()
	db1 := filepath.Join(dir, "message_0.db")
	db2 := filepath.Join(dir, "message_1.db")
	table := TableName("alice")

	createMessageDB(t, db1, table, []messageRow{
		{LocalID: 1, SortSeq: 1001, CreateTime: 100, Status: 4, Content: "first"},
		{LocalID: 3, SortSeq: 3001, CreateTime: 300, Status: 2, Content: "third"},
	})
	createMessageDB(t, db2, table, []messageRow{
		{LocalID: 2, SortSeq: 2001, CreateTime: 200, Status: 4, Content: "second"},
		{LocalID: 4, SortSeq: 4001, CreateTime: 400, Status: 4, Content: "outside"},
	})

	got, err := NewService([]string{db1, db2}).List(context.Background(), QueryOptions{
		Username: "alice",
		Start:    100,
		End:      300,
		HasStart: true,
		HasEnd:   true,
		Limit:    2,
		Offset:   1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2: %+v", len(got), got)
	}
	if got[0].Content != "second" || got[0].CreateTime != 200 {
		t.Fatalf("first paged message = %+v, want second at 200", got[0])
	}
	if got[0].ChatUsername != "alice" || got[0].FromUsername != "alice" || got[0].Direction != "in" || got[0].IsSelf {
		t.Fatalf("first paged identity = %+v, want incoming alice message", got[0])
	}
	if got[1].Content != "third" || got[1].FromUsername != "self" || got[1].Direction != "out" || !got[1].IsSelf {
		t.Fatalf("second paged message = %+v, want self third", got[1])
	}
	if got[1].Source != nil {
		t.Fatalf("source should be omitted by default: %+v", got[1].Source)
	}
}

func TestApplyChatInfoFillsMetadata(t *testing.T) {
	list := []Message{{ChatUsername: "room@chatroom"}}
	ApplyChatInfo(list, map[string]ChatInfo{
		"room@chatroom": {
			Username:    "room@chatroom",
			Kind:        "chatroom",
			DisplayName: "AI Room",
			Alias:       "room-alias",
			Remark:      "AI Room",
			NickName:    "Original Room",
		},
	})
	if list[0].ChatKind != "chatroom" || list[0].ChatDisplayName != "AI Room" || list[0].ChatAlias != "room-alias" || list[0].ChatRemark != "AI Room" || list[0].ChatNickName != "Original Room" {
		t.Fatalf("chat metadata not applied: %+v", list[0])
	}
}

func TestApplyChatInfoFallbackForMissingContact(t *testing.T) {
	list := []Message{{ChatUsername: "alice"}}
	ApplyChatInfo(list, nil)
	if list[0].ChatKind != ChatKindUnknown || list[0].ChatDisplayName != "alice" {
		t.Fatalf("missing contact fallback = %+v", list[0])
	}
}

func TestListParsesChatroomSenderPrefix(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "message_0.db")
	table := TableName("room@chatroom")
	createMessageDB(t, db, table, []messageRow{
		{LocalID: 1, SortSeq: 1001, CreateTime: 100, Status: 4, Content: "wxid_sender:\nhello room"},
	})

	got, err := NewService([]string{db}).List(context.Background(), QueryOptions{Username: "room@chatroom"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	if got[0].FromUsername != "wxid_sender" || got[0].Content != "wxid_sender:\nhello room" || !got[0].IsChatroom {
		t.Fatalf("unexpected group message normalization: %+v", got[0])
	}
	if got[0].ContentDetail["text"] != "hello room" {
		t.Fatalf("unexpected group message detail: %+v", got[0].ContentDetail)
	}
}

func TestListNormalizesChatroomImageDetailWithoutChangingRawContent(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "message_0.db")
	table := TableName("room@chatroom")
	content := `wxid_sender:
<?xml version="1.0"?>
<msg>
  <img cdnthumburl="thumb-file-id" cdnthumblength="3768" cdnthumbheight="432" cdnthumbwidth="173" cdnmidheight="0" cdnmidwidth="0" cdnmidimgurl="mid-file-id" length="446018" md5="90acf8a0ba4bbc0c22218b6887797d8b" hevc_mid_size="185480" originsourcemd5="a56a063b789f8436d209bb9d472ed96e"></img>
</msg>`
	createMessageDB(t, db, table, []messageRow{
		{LocalID: 1, SortSeq: 1001, CreateTime: 100, LocalType: 3, Status: 4, Content: content},
	})

	got, err := NewService([]string{db}).List(context.Background(), QueryOptions{Username: "room@chatroom"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	if got[0].Content != content || got[0].FromUsername != "wxid_sender" {
		t.Fatalf("raw content or sender changed unexpectedly: %+v", got[0])
	}
	detail := got[0].ContentDetail
	if detail["type"] != "image" || detail["text"] != "[图片]" {
		t.Fatalf("unexpected image detail identity: %+v", detail)
	}
	if detail["md5"] != "90acf8a0ba4bbc0c22218b6887797d8b" || detail["length"] != "446018" || detail["cdn_thumb_width"] != "173" || detail["cdn_thumb_height"] != "432" {
		t.Fatalf("missing image metadata: %+v", detail)
	}
	if detail["cdn_mid_width"] != "" || detail["cdn_mid_height"] != "" {
		t.Fatalf("zero image dimensions should be omitted: %+v", detail)
	}
}

func TestListAddsImagePathWithoutChangingImageText(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "message_0.db")
	chat := "alice"
	table := TableName(chat)
	dataDir := filepath.Join(dir, "wxid_owner_bcc2", "db_storage")
	imageDir := filepath.Join(dir, "wxid_owner_bcc2", "Message", "MessageTemp", md5HexForTest(chat), "Image")
	if err := os.MkdirAll(imageDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(imageDir, "71700000000_.pic_thumb.jpg")
	writePNGForTest(t, imagePath, 2, 3)

	createMessageDB(t, db, table, []messageRow{
		{LocalID: 7, SortSeq: 1001, CreateTime: 1700000000, LocalType: 3, Status: 4, Content: "<msg><img /></msg>"},
	})
	resolver := media.NewResolver(dataDir, filepath.Join(dir, "cache"))

	got, err := NewService([]string{db}).List(context.Background(), QueryOptions{Username: chat, MediaResolver: &resolver})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	detail := got[0].ContentDetail
	if detail["text"] != "[图片]" {
		t.Fatalf("image text = %q, want [图片]; detail=%+v", detail["text"], detail)
	}
	if detail["path"] != imagePath || detail["width"] != "2" || detail["height"] != "3" || detail["media_status"] != "resolved" {
		t.Fatalf("unexpected resolved image detail: %+v", detail)
	}
}

func TestListAddsVideoPathAndParsesVideoMetadata(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "message_0.db")
	chat := "alice"
	table := TableName(chat)
	dataDir := filepath.Join(dir, "wxid_owner_bcc2", "db_storage")
	videoDir := filepath.Join(dir, "wxid_owner_bcc2", "Message", "MessageTemp", md5HexForTest(chat), "Video")
	if err := os.MkdirAll(videoDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	videoPath := filepath.Join(videoDir, "71700000000.mp4")
	writeMP4ForTest(t, videoPath)
	thumbPath := filepath.Join(videoDir, "71700000000_thumb.jpg")
	writePNGForTest(t, thumbPath, 4, 5)

	content := `<msg><videomsg aeskey="secret-aes-key" cdnvideourl="video-file-id" cdnthumburl="thumb-file-id" length="5007511" playlength="43" cdnthumblength="3762" cdnthumbwidth="288" cdnthumbheight="162" fromusername="wxid_sender" md5="f4c5335335d0a326df203b9bb51aa503" newmd5="de3fa0a024bbb1cb9fc6e2b65843a063" originsourcemd5="735f7ff8967a783bc7b85178490852ff" /></msg>`
	createMessageDB(t, db, table, []messageRow{
		{LocalID: 7, SortSeq: 1001, CreateTime: 1700000000, LocalType: 43, Status: 4, Content: content},
	})
	resolver := media.NewResolver(dataDir, filepath.Join(dir, "cache"))

	got, err := NewService([]string{db}).List(context.Background(), QueryOptions{Username: chat, MediaResolver: &resolver})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	detail := got[0].ContentDetail
	if detail["type"] != "video" || detail["text"] != "[视频]" {
		t.Fatalf("unexpected video detail identity: %+v", detail)
	}
	if detail["md5"] != "f4c5335335d0a326df203b9bb51aa503" || detail["new_md5"] != "de3fa0a024bbb1cb9fc6e2b65843a063" || detail["play_length"] != "43" || detail["cdn_thumb_width"] != "288" {
		t.Fatalf("missing video metadata: %+v", detail)
	}
	if detail["aes_key"] != "" || detail["cdn_thumb_aes_key"] != "" {
		t.Fatalf("secret video keys should not be copied into content_detail: %+v", detail)
	}
	if detail["path"] != videoPath || detail["thumbnail_path"] != thumbPath || detail["media_status"] != "resolved" || detail["thumbnail"] != "true" {
		t.Fatalf("unexpected resolved video detail: %+v", detail)
	}
}

func TestSearchTextOmitsRawXMLSecrets(t *testing.T) {
	content := `<msg><videomsg aeskey="secret-aes-key" cdnvideourl="video-file-id" cdnthumburl="thumb-file-id" playlength="43" /></msg>`
	got := SearchText("alice", 43, 4, 0, "", content)
	if strings.Contains(got, "secret-aes-key") || strings.Contains(got, "aeskey") {
		t.Fatalf("search text should not include raw XML AES keys: %q", got)
	}
	if !strings.Contains(got, "[视频]") || !strings.Contains(got, "video-file-id") {
		t.Fatalf("search text should retain safe parsed detail: %q", got)
	}
}

func TestListNormalizesVOIPDetail(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "message_31.db")
	table := TableName("alice")
	content := `<voipmsg type="VoIPBubbleMsg"><VoIPBubbleMsg><msg><![CDATA[通话时长 04:40]]></msg>
<room_type>1</room_type>
<red_dot>false</red_dot>
<roomid>244884889767588661</roomid>
<roomkey>0</roomkey>
<inviteid>1778760291</inviteid>
<msg_type>100</msg_type>
<timestamp>1778760581834</timestamp>
<identity><![CDATA[7102943500496325869]]></identity>
<duration>0</duration>
<inviteid64>1778760291</inviteid64>
<business>1</business>
<caller_memberid>0</caller_memberid>
<callee_memberid>1</callee_memberid>
</VoIPBubbleMsg></voipmsg>`
	createMessageDB(t, db, table, []messageRow{{
		LocalID:    1726,
		SortSeq:    1778760581000,
		CreateTime: 1778760581,
		LocalType:  50,
		Status:     2,
		Content:    content,
	}})

	got, err := NewService([]string{db}).List(context.Background(), QueryOptions{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	if got[0].Content != content {
		t.Fatalf("content was modified:\n got: %q\nwant: %q", got[0].Content, content)
	}
	detail := got[0].ContentDetail
	if detail["type"] != "voip" || detail["text"] != "[语音通话|通话时长 04:40]" {
		t.Fatalf("unexpected voip detail identity: %+v", detail)
	}
	if detail["call_text"] != "通话时长 04:40" || detail["duration_text"] != "04:40" || detail["duration_seconds"] != "280" {
		t.Fatalf("missing voip duration detail: %+v", detail)
	}
	if detail["voip_type"] != "VoIPBubbleMsg" || detail["msg_type"] != "100" || detail["business"] != "1" {
		t.Fatalf("missing voip message metadata: %+v", detail)
	}
	if detail["roomid"] != "" || detail["identity"] != "" || detail["inviteid"] != "" {
		t.Fatalf("internal voip identifiers should not be copied into content_detail: %+v", detail)
	}
}

func TestListFiltersAfterSeq(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "message_0.db")
	table := TableName("alice")
	createMessageDB(t, db, table, []messageRow{
		{LocalID: 1, SortSeq: 1001, CreateTime: 100, Content: "first"},
		{LocalID: 2, SortSeq: 2001, CreateTime: 200, Content: "second"},
		{LocalID: 3, SortSeq: 3001, CreateTime: 300, Content: "third"},
	})

	got, err := NewService([]string{db}).List(context.Background(), QueryOptions{
		Username:    "alice",
		AfterSeq:    2001,
		HasAfterSeq: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Seq != 3001 || got[0].Content != "third" {
		t.Fatalf("after-seq result = %+v, want only third", got)
	}
}

func TestListInfersSelfFromRealSenderID(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "message_0.db")
	table := TableName("alice")
	createMessageDB(t, db, table, []messageRow{{
		LocalID:      1,
		SortSeq:      1001,
		CreateTime:   100,
		Status:       0,
		RealSenderID: 2,
		Content:      "self greeting",
	}})

	got, err := NewService([]string{db}).List(context.Background(), QueryOptions{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	if got[0].FromUsername != "self_user" || got[0].Direction != "out" || !got[0].IsSelf {
		t.Fatalf("sender = %+v, want real sender username/out inferred from real_sender_id", got[0])
	}
}

func TestListInfersPrivateSelfWhenTalkerDiffersFromSenderName(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "message_0.db")
	table := TableName("alice")
	createMessageDB(t, db, table, []messageRow{{
		LocalID:      1,
		SortSeq:      1001,
		CreateTime:   100,
		Status:       0,
		RealSenderID: 9,
		SenderName:   "owner_wxid",
		Content:      "private self greeting",
	}})

	got, err := NewService([]string{db}).List(context.Background(), QueryOptions{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	if got[0].FromUsername != "owner_wxid" || got[0].Direction != "out" || !got[0].IsSelf {
		t.Fatalf("sender = %+v, want chatlog-style private self inference", got[0])
	}
}

func TestListDecodesZstdContentAndSplitsType(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "message_0.db")
	table := TableName("alice")
	createMessageDB(t, db, table, []messageRow{{
		LocalID:     1,
		SortSeq:     1001,
		CreateTime:  100,
		LocalType:   21474836529,
		ContentBlob: zstdCompress(t, "hello zstd"),
	}})

	got, err := NewService([]string{db}).List(context.Background(), QueryOptions{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	if got[0].Content != "hello zstd" || got[0].ContentEncoding != "zstd" {
		t.Fatalf("unexpected zstd content: %+v", got[0])
	}
	if got[0].Type != 49 || got[0].SubType != 5 {
		t.Fatalf("type split = (%d,%d), want (49,5)", got[0].Type, got[0].SubType)
	}
}

func TestListNormalizesAppMsgLink(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "message_0.db")
	table := TableName("alice")
	content := `<?xml version="1.0"?>
<msg>
  <appmsg>
    <title>Readable title</title>
    <des>Readable desc</des>
    <type>5</type>
    <url>https://example.com/a?x=1&amp;y=2</url>
    <sourceusername>gh_test</sourceusername>
    <sourcedisplayname>Source Name</sourcedisplayname>
  </appmsg>
</msg>`
	createMessageDB(t, db, table, []messageRow{{
		LocalID:    1,
		SortSeq:    1001,
		CreateTime: 100,
		LocalType:  21474836529,
		Content:    content,
	}})

	got, err := NewService([]string{db}).List(context.Background(), QueryOptions{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	if got[0].Content != content {
		t.Fatalf("content was modified:\n got: %q\nwant: %q", got[0].Content, content)
	}
	wantDetailText := "[链接|Readable title](https://example.com/a?x=1&y=2)\nReadable desc"
	if got[0].ContentDetail["type"] != "link" || got[0].ContentDetail["text"] != wantDetailText {
		t.Fatalf("unexpected normalized link detail: %+v", got[0].ContentDetail)
	}
	if got[0].ContentDetail["title"] != "Readable title" || got[0].ContentDetail["url"] != "https://example.com/a?x=1&y=2" {
		t.Fatalf("unexpected link details: %+v", got[0].ContentDetail)
	}
}

func TestListNormalizesQuoteReferMessageDetail(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "message_31.db")
	table := TableName("amy76868")
	quotedContent := `<?xml version="1.0"?>
<msg>
  <appmsg>
    <title>壹心二期人力预算.md</title>
    <type>6</type>
    <appattach>
      <totallen>1358</totallen>
      <fileext>md</fileext>
      <aeskey>secret-aes-key</aeskey>
    </appattach>
    <md5>c1545a2b5e72e5a391d6261993bbaf7a</md5>
  </appmsg>
</msg>`
	content := fmt.Sprintf(`<?xml version="1.0"?>
<msg>
  <appmsg>
    <title>整体还行，水分不大</title>
    <type>57</type>
    <refermsg>
      <chatusr>amy76868</chatusr>
      <type>49</type>
      <createtime>1779336351</createtime>
      <displayname>Amy谢慧</displayname>
      <svrid>656050813261587710</svrid>
      <fromusr>amy76868</fromusr>
      <content>%s</content>
    </refermsg>
  </appmsg>
</msg>`, escapeXMLTextForTest(t, quotedContent))
	createMessageDB(t, db, table, []messageRow{{
		LocalID:    397,
		SortSeq:    1779347138000,
		CreateTime: 1779347138,
		LocalType:  (int64(messageSubTypeQuote) << 32) | int64(messageTypeShare),
		Status:     4,
		Content:    content,
	}})

	got, err := NewService([]string{db}).List(context.Background(), QueryOptions{Username: "amy76868"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	detail := got[0].ContentDetail
	if detail["type"] != "quote" || detail["text"] != "> [引用]\n整体还行，水分不大\n> [原消息]\n[文件|壹心二期人力预算.md]" {
		t.Fatalf("unexpected quote detail identity: %+v", detail)
	}
	if detail["refer_chat_username"] != "amy76868" || detail["refer_from_username"] != "amy76868" || detail["refer_server_id"] != "656050813261587710" {
		t.Fatalf("missing refer message identity: %+v", detail)
	}
	if detail["refer_type"] != "49" || detail["refer_content_type"] != "file" || detail["refer_app_type"] != "6" {
		t.Fatalf("missing refer message type detail: %+v", detail)
	}
	if detail["refer_text"] != "[文件|壹心二期人力预算.md]" || detail["refer_title"] != "壹心二期人力预算.md" {
		t.Fatalf("missing quoted file title: %+v", detail)
	}
	if detail["refer_file_ext"] != "md" || detail["refer_total_len"] != "1358" || detail["refer_md5"] != "c1545a2b5e72e5a391d6261993bbaf7a" {
		t.Fatalf("missing quoted file metadata: %+v", detail)
	}
	for key, value := range detail {
		if strings.Contains(strings.ToLower(key), "aes") || strings.Contains(value, "secret-aes-key") {
			t.Fatalf("secret key leaked into quote detail %s=%q: %+v", key, value, detail)
		}
	}
}

func TestListNormalizesChatRecordDetail(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "message_31.db")
	table := TableName("amy76868")
	recordItem := `<recordinfo><fromscene>0</fromscene><favcreatetime>1779338631</favcreatetime><title>本伟的聊天记录</title><desc>本伟: [小程序] Peet's Cafe皮爷咖啡·轻餐(凤凰汇店)
本伟: 明天4点这里
本伟: 地铁到三元桥出来，直接可以走到凤凰汇地下，再上来一层就好</desc><datalist count="3"><dataitem datatype="19" dataid="mini1"><sourcename>本伟</sourcename><sourcetime>2026-05-21 12:40</sourcetime><datatitle>Peet's Cafe皮爷咖啡·轻餐(凤凰汇店)</datatitle><cdnthumbkey>thumb-secret</cdnthumbkey><dataitemsource><hashusername>hash-secret</hashusername></dataitemsource><appbranditem><username>gh_bc5b635c05c4@app</username><appid>wx734c1ad7b3562129</appid><sourcedisplayname>大众点评美食电影运动旅游门票</sourcedisplayname></appbranditem></dataitem><dataitem datatype="1" dataid="text1"><sourcename>本伟</sourcename><sourcetime>2026-05-21 12:40</sourcetime><datadesc>明天4点这里</datadesc></dataitem><dataitem datatype="1" dataid="text2"><sourcename>本伟</sourcename><sourcetime>2026-05-21 12:41</sourcetime><datadesc>地铁到三元桥出来，直接可以走到凤凰汇地下，再上来一层就好</datadesc></dataitem></datalist></recordinfo>`
	content := fmt.Sprintf(`<?xml version="1.0"?>
<msg>
  <appmsg>
    <title>本伟的聊天记录</title>
    <des>本伟: [小程序] Peet's Cafe皮爷咖啡·轻餐(凤凰汇店)
本伟: 明天4点这里
本伟: 地铁到三元桥出来，直接可以走到凤凰汇地下，再上来一层就好</des>
    <type>19</type>
    <recorditem><![CDATA[%s]]></recorditem>
  </appmsg>
</msg>`, recordItem)
	createMessageDB(t, db, table, []messageRow{{
		LocalID:    394,
		SortSeq:    1779338631000,
		CreateTime: 1779338631,
		LocalType:  (int64(messageSubTypeChatRecord) << 32) | int64(messageTypeShare),
		Status:     4,
		Content:    content,
	}})

	got, err := NewService([]string{db}).List(context.Background(), QueryOptions{Username: "amy76868"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	detail := got[0].ContentDetail
	if detail["type"] != "chat_record" || detail["app_type"] != "19" || detail["record_count"] != "3" {
		t.Fatalf("unexpected chat record identity: %+v", detail)
	}
	wantText := "[聊天记录|本伟的聊天记录]\n本伟: [小程序|Peet's Cafe皮爷咖啡·轻餐(凤凰汇店)]\n本伟: 明天4点这里\n本伟: 地铁到三元桥出来，直接可以走到凤凰汇地下，再上来一层就好"
	if detail["text"] != wantText || detail["record_text"] != strings.TrimPrefix(wantText, "[聊天记录|本伟的聊天记录]\n") {
		t.Fatalf("unexpected chat record text: %+v", detail)
	}
	if !strings.Contains(detail["record_items_json"], `"type":"mini_program"`) || !strings.Contains(detail["record_items_json"], `"app_name":"大众点评美食电影运动旅游门票"`) {
		t.Fatalf("missing parsed record item details: %+v", detail)
	}
	for key, value := range detail {
		if strings.Contains(value, "thumb-secret") || strings.Contains(value, "hash-secret") || strings.Contains(strings.ToLower(key), "cdnthumbkey") {
			t.Fatalf("internal record metadata leaked into detail %s=%q: %+v", key, value, detail)
		}
	}
}

func TestListIncludesSourceWhenRequested(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "message_31.db")
	table := TableName("alice")
	createMessageDB(t, db, table, []messageRow{{LocalID: 7, SortSeq: 1001, CreateTime: 100, Content: "hello"}})

	got, err := NewService([]string{db}).List(context.Background(), QueryOptions{Username: "alice", IncludeSource: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Source == nil {
		t.Fatalf("source missing: %+v", got)
	}
	if got[0].Source.DB != "message_31.db" || got[0].Source.Table != table || got[0].Source.LocalID != 7 {
		t.Fatalf("unexpected source: %+v", got[0].Source)
	}
}

func TestListReturnsEmptyWhenTableMissing(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "message_0.db")
	createMessageDB(t, db, TableName("bob"), []messageRow{{LocalID: 1, SortSeq: 1, CreateTime: 1, Content: "bob"}})

	got, err := NewService([]string{db}).List(context.Background(), QueryOptions{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got messages for missing table: %+v", got)
	}
}

func TestSearchFiltersDecodedMessageContent(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "message_0.db")
	table := TableName("alice")
	createMessageDB(t, db, table, []messageRow{
		{LocalID: 1, SortSeq: 101, CreateTime: 1700000000, Status: 4, Content: "ordinary note"},
		{LocalID: 2, SortSeq: 102, CreateTime: 1700000200, Status: 4, ContentBlob: zstdCompress(t, "AI project update")},
	})

	got, total, err := NewService([]string{db}).Search(context.Background(), SearchOptions{
		Chats: []ChatInfo{{
			Username:    "alice",
			Kind:        "friend",
			DisplayName: "Alice",
		}},
		Query: "project",
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(got) != 1 {
		t.Fatalf("got total=%d len=%d, want 1 result: %+v", total, len(got), got)
	}
	if got[0].Content != "AI project update" || got[0].ContentEncoding != "zstd" || got[0].ChatDisplayName != "Alice" {
		t.Fatalf("unexpected search result: %+v", got[0])
	}
}

type messageRow struct {
	LocalID      int64
	SortSeq      int64
	CreateTime   int64
	Status       int64
	LocalType    int64
	RealSenderID int64
	SenderName   string
	Content      string
	ContentBlob  []byte
}

func createMessageDB(t *testing.T, path string, table string, rows []messageRow) {
	t.Helper()
	db := sqlitetest.CreateDB(t, path, fmt.Sprintf(`
CREATE TABLE %s (
  local_id INTEGER PRIMARY KEY,
  server_id INTEGER,
  local_type INTEGER,
  sort_seq INTEGER,
  real_sender_id INTEGER,
  create_time INTEGER,
  status INTEGER,
  message_content BLOB,
  WCDB_CT_message_content INTEGER
);
CREATE TABLE Name2Id(user_name TEXT PRIMARY KEY, is_session INTEGER);
INSERT INTO Name2Id(rowid, user_name, is_session) VALUES (2, 'self_user', 0);
`, sqlitedb.QuoteIdent(table)))
	defer db.Close()
	for _, row := range rows {
		localType := row.LocalType
		if localType == 0 {
			localType = 1
		}
		realSenderID := row.RealSenderID
		content := []byte(row.Content)
		if row.ContentBlob != nil {
			content = row.ContentBlob
		}
		if row.SenderName != "" && realSenderID != 0 && realSenderID != 2 {
			sqlitetest.Exec(t, db, "INSERT INTO Name2Id(rowid, user_name, is_session) VALUES (?, ?, 0);", realSenderID, row.SenderName)
		}
		sqlitetest.Exec(t, db,
			fmt.Sprintf("INSERT INTO %s (local_id, server_id, local_type, sort_seq, real_sender_id, create_time, status, message_content, WCDB_CT_message_content) VALUES (?, 0, ?, ?, ?, ?, ?, ?, 0);", sqlitedb.QuoteIdent(table)),
			row.LocalID,
			localType,
			row.SortSeq,
			realSenderID,
			row.CreateTime,
			row.Status,
			content,
		)
	}
}

func zstdCompress(t *testing.T, text string) []byte {
	t.Helper()
	encoder, err := zstdpkg.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	return encoder.EncodeAll([]byte(text), nil)
}

func md5HexForTest(value string) string {
	sum := md5.Sum([]byte(value))
	return hex.EncodeToString(sum[:])
}

func writePNGForTest(t *testing.T, path string, width int, height int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: 0x22, G: 0x66, B: 0xaa, A: 0xff})
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

func writeMP4ForTest(t *testing.T, path string) {
	t.Helper()
	data := []byte{0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0x00, 0x00, 0x00, 0x00}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func escapeXMLTextForTest(t *testing.T, value string) string {
	t.Helper()
	var out strings.Builder
	if err := xml.EscapeText(&out, []byte(value)); err != nil {
		t.Fatal(err)
	}
	return out.String()
}
