package messages

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	messageTypeText      = 1
	messageTypeImage     = 3
	messageTypeVoice     = 34
	messageTypeCard      = 42
	messageTypeVideo     = 43
	messageTypeAnimation = 47
	messageTypeLocation  = 48
	messageTypeShare     = 49
	messageTypeVOIP      = 50
	messageTypeSystem    = 10000

	messageSubTypeText             = 1
	messageSubTypeLink             = 4
	messageSubTypeLink2            = 5
	messageSubTypeFile             = 6
	messageSubTypeGIF              = 8
	messageSubTypeChatRecord       = 19
	messageSubTypeMiniProgram      = 33
	messageSubTypeMiniProgram2     = 36
	messageSubTypeChannel          = 51
	messageSubTypeQuote            = 57
	messageSubTypePat              = 62
	messageSubTypeChannelLive      = 63
	messageSubTypeMusic            = 92
	messageSubTypePay              = 2000
	messageSubTypeRedEnvelope      = 2001
	messageSubTypeRedEnvelopeCover = 2003
)

type normalizedContent struct {
	Type    string
	Text    string
	Details map[string]string
}

func (c normalizedContent) Detail() map[string]string {
	detail := make(map[string]string, len(c.Details)+2)
	if c.Type != "" {
		detail["type"] = c.Type
	}
	if c.Text != "" {
		detail["text"] = c.Text
	}
	for key, value := range c.Details {
		if value != "" {
			detail[key] = value
		}
	}
	if len(detail) == 0 {
		return nil
	}
	return detail
}

type mediaMsg struct {
	XMLName  xml.Name    `xml:"msg"`
	Username string      `xml:"username,attr"`
	Nickname string      `xml:"nickname,attr"`
	Alias    string      `xml:"alias,attr"`
	Province string      `xml:"province,attr"`
	City     string      `xml:"city,attr"`
	Sex      string      `xml:"sex,attr"`
	SmallImg string      `xml:"smallheadimgurl,attr"`
	BigImg   string      `xml:"bigheadimgurl,attr"`
	App      appMsg      `xml:"appmsg"`
	Image    imageMsg    `xml:"img"`
	Video    videoMsg    `xml:"videomsg"`
	Voice    voiceMsg    `xml:"voicemsg"`
	Location locationMsg `xml:"location"`
	Emoji    emojiMsg    `xml:"emoji"`
}

type appMsg struct {
	Type              int         `xml:"type"`
	Title             string      `xml:"title"`
	Des               string      `xml:"des"`
	URL               string      `xml:"url"`
	LowURL            string      `xml:"lowurl"`
	DataURL           string      `xml:"dataurl"`
	LowDataURL        string      `xml:"lowdataurl"`
	ThumbURL          string      `xml:"thumburl"`
	MD5               string      `xml:"md5"`
	SourceUsername    string      `xml:"sourceusername"`
	SourceDisplayName string      `xml:"sourcedisplayname"`
	AppAttach         *appAttach  `xml:"appattach"`
	Refer             *referMsg   `xml:"refermsg"`
	RecordItem        string      `xml:"recorditem"`
	WeAppInfo         *weAppInfo  `xml:"weappinfo"`
	FinderFeed        *finderFeed `xml:"finderFeed"`
	FinderLive        *finderLive `xml:"finderLive"`
	WCPayInfo         *wcPayInfo  `xml:"wcpayinfo"`
}

type referMsg struct {
	ChatUser    string `xml:"chatusr"`
	Type        string `xml:"type"`
	LocalID     string `xml:"msgid"`
	CreateTime  string `xml:"createtime"`
	DisplayName string `xml:"displayname"`
	ServerID    string `xml:"svrid"`
	FromUser    string `xml:"fromusr"`
	Content     string `xml:"content"`
}

type appAttach struct {
	FileExt      string `xml:"fileext"`
	TotalLen     string `xml:"totallen"`
	AttachID     string `xml:"attachid"`
	CDNAttachURL string `xml:"cdnattachurl"`
}

type weAppInfo struct {
	Username          string `xml:"username"`
	AppID             string `xml:"appid"`
	PagePath          string `xml:"pagepath"`
	SourceDisplayName string `xml:"sourcedisplayname"`
	Type              string `xml:"type"`
	PkgType           string `xml:"pkgtype"`
	Version           string `xml:"version"`
	IconURL           string `xml:"iconurl"`
}

type recordInfo struct {
	Title    string         `xml:"title"`
	Desc     string         `xml:"desc"`
	DataList recordDataList `xml:"datalist"`
}

type recordDataList struct {
	Count string           `xml:"count,attr"`
	Items []recordDataItem `xml:"dataitem"`
}

type recordDataItem struct {
	DataType         string             `xml:"datatype,attr"`
	DataID           string             `xml:"dataid,attr"`
	SourceName       string             `xml:"sourcename"`
	SourceHeadURL    string             `xml:"sourceheadurl"`
	SourceTime       string             `xml:"sourcetime"`
	DataTitle        string             `xml:"datatitle"`
	DataDesc         string             `xml:"datadesc"`
	ThumbSize        string             `xml:"thumbsize"`
	ThumbFullMD5     string             `xml:"thumbfullmd5"`
	CDNThumbURL      string             `xml:"cdnthumburl"`
	DataFmt          string             `xml:"datafmt"`
	DataSize         string             `xml:"datasize"`
	FullMD5          string             `xml:"fullmd5"`
	SrcMsgLocalID    string             `xml:"srcMsgLocalid"`
	SrcMsgCreateTime string             `xml:"srcMsgCreateTime"`
	AppBrand         recordAppBrandItem `xml:"appbranditem"`
}

type recordAppBrandItem struct {
	Username          string `xml:"username"`
	AppID             string `xml:"appid"`
	SourceDisplayName string `xml:"sourcedisplayname"`
	Type              string `xml:"type"`
}

type recordItemDetail struct {
	Type             string `json:"type,omitempty"`
	DataType         string `json:"data_type,omitempty"`
	DataID           string `json:"data_id,omitempty"`
	SourceName       string `json:"source_name,omitempty"`
	SourceTime       string `json:"source_time,omitempty"`
	Title            string `json:"title,omitempty"`
	Desc             string `json:"desc,omitempty"`
	ThumbURL         string `json:"thumb_url,omitempty"`
	ThumbMD5         string `json:"thumb_md5,omitempty"`
	MD5              string `json:"md5,omitempty"`
	Size             string `json:"size,omitempty"`
	AppName          string `json:"app_name,omitempty"`
	AppID            string `json:"app_id,omitempty"`
	AppUsername      string `json:"app_username,omitempty"`
	SrcMsgLocalID    string `json:"src_msg_local_id,omitempty"`
	SrcMsgCreateTime string `json:"src_msg_create_time,omitempty"`
	MediaStatus      string `json:"media_status,omitempty"`
	Path             string `json:"path,omitempty"`
	SourcePath       string `json:"source_path,omitempty"`
	ThumbnailPath    string `json:"thumbnail_path,omitempty"`
	MediaReason      string `json:"media_reason,omitempty"`
	Width            string `json:"width,omitempty"`
	Height           string `json:"height,omitempty"`
}

type imageMsg struct {
	CDNThumbURL    string `xml:"cdnthumburl,attr"`
	CDNThumbLength string `xml:"cdnthumblength,attr"`
	CDNThumbWidth  string `xml:"cdnthumbwidth,attr"`
	CDNThumbHeight string `xml:"cdnthumbheight,attr"`
	CDNMidImgURL   string `xml:"cdnmidimgurl,attr"`
	CDNMidWidth    string `xml:"cdnmidwidth,attr"`
	CDNMidHeight   string `xml:"cdnmidheight,attr"`
	CDNHDWidth     string `xml:"cdnhdwidth,attr"`
	CDNHDHeight    string `xml:"cdnhdheight,attr"`
	Length         string `xml:"length,attr"`
	MD5            string `xml:"md5,attr"`
	HEVCMidSize    string `xml:"hevc_mid_size,attr"`
	OriginMD5      string `xml:"originsourcemd5,attr"`
}

type videoMsg struct {
	AESKey            string `xml:"aeskey,attr"`
	CDNVideoURL       string `xml:"cdnvideourl,attr"`
	CDNThumbAESKey    string `xml:"cdnthumbaeskey,attr"`
	CDNThumbURL       string `xml:"cdnthumburl,attr"`
	Length            string `xml:"length,attr"`
	PlayLength        string `xml:"playlength,attr"`
	CDNThumbLength    string `xml:"cdnthumblength,attr"`
	CDNThumbWidth     string `xml:"cdnthumbwidth,attr"`
	CDNThumbHeight    string `xml:"cdnthumbheight,attr"`
	FromUsername      string `xml:"fromusername,attr"`
	MD5               string `xml:"md5,attr"`
	NewMD5            string `xml:"newmd5,attr"`
	RawMD5            string `xml:"rawmd5,attr"`
	RawLength         string `xml:"rawlength,attr"`
	CDNRawVideoURL    string `xml:"cdnrawvideourl,attr"`
	CDNRawVideoAESKey string `xml:"cdnrawvideoaeskey,attr"`
	OriginSourceMD5   string `xml:"originsourcemd5,attr"`
	IsPlaceholder     string `xml:"isplaceholder,attr"`
	IsAd              string `xml:"isad,attr"`
}

type finderFeed struct {
	Desc      string          `xml:"desc"`
	Nickname  string          `xml:"nickname"`
	MediaList finderMediaList `xml:"mediaList"`
}

type finderMediaList struct {
	Media []finderMedia `xml:"media"`
}

type finderMedia struct {
	URL string `xml:"url"`
}

type finderLive struct {
	Desc string `xml:"desc"`
}

type wcPayInfo struct {
	PaySubType int    `xml:"paysubtype"`
	FeeDesc    string `xml:"feedesc"`
	PayMemo    string `xml:"pay_memo"`
}

type locationMsg struct {
	X        string `xml:"x,attr"`
	Y        string `xml:"y,attr"`
	Label    string `xml:"label,attr"`
	CityName string `xml:"cityname,attr"`
}

type emojiMsg struct {
	CdnURL    string `xml:"cdnurl,attr"`
	MD5       string `xml:"md5,attr"`
	Len       string `xml:"len,attr"`
	Width     string `xml:"width,attr"`
	Height    string `xml:"height,attr"`
	Type      string `xml:"type,attr"`
	ProductID string `xml:"productid,attr"`
	AuthKey   string `xml:"authkey,attr"`
	AESKey    string `xml:"aeskey,attr"`
}

type voiceMsg struct {
	VoiceLength string `xml:"voicelength,attr"`
	Length      string `xml:"length,attr"`
	Format      string `xml:"format,attr"`
}

type sysMsg struct {
	XMLName xml.Name
	Type    string       `xml:"type,attr"`
	Content string       `xml:",chardata"`
	Revoke  revokeSysMsg `xml:"revokemsg"`
}

type revokeSysMsg struct {
	Content    string `xml:"content"`
	RevokeTime string `xml:"revoketime"`
}

type voipMsg struct {
	Type   string        `xml:"type,attr"`
	Bubble voipBubbleMsg `xml:"VoIPBubbleMsg"`
}

type voipBubbleMsg struct {
	Msg      string `xml:"msg"`
	MsgType  string `xml:"msg_type"`
	Duration string `xml:"duration"`
	Business string `xml:"business"`
}

var clockDurationPattern = regexp.MustCompile(`\b\d{1,2}(?::\d{2}){1,2}\b`)

func normalizeContent(msgType int64, subType int64, raw string) normalizedContent {
	switch msgType {
	case messageTypeText:
		return normalizedContent{Type: "text", Text: raw}
	case messageTypeImage:
		return normalizeImage(raw)
	case messageTypeVoice:
		return normalizeVoice(raw)
	case messageTypeCard:
		return normalizeCard(raw)
	case messageTypeVideo:
		return normalizeVideo(raw)
	case messageTypeAnimation:
		return normalizeEmoji(raw)
	case messageTypeLocation:
		return normalizeLocation(raw)
	case messageTypeShare:
		return normalizeShare(subType, raw)
	case messageTypeVOIP:
		return normalizeVOIP(raw)
	case messageTypeSystem:
		return normalizeSystem(raw)
	default:
		return normalizedContent{Type: "unknown", Text: strings.TrimSpace(raw)}
	}
}

func normalizeVOIP(raw string) normalizedContent {
	msg, ok := parseVOIP(raw)
	if !ok {
		return normalizedContent{Type: "voip", Text: "[语音通话]"}
	}

	details := map[string]string{}
	callText := strings.TrimSpace(msg.Bubble.Msg)
	putIfNotEmpty(details, "call_text", callText)
	putIfNotEmpty(details, "voip_type", msg.Type)
	putIfNotEmpty(details, "msg_type", msg.Bubble.MsgType)
	putIfNotEmpty(details, "business", msg.Bubble.Business)

	durationText := clockDurationPattern.FindString(callText)
	putIfNotEmpty(details, "duration_text", durationText)
	if seconds, ok := parsePositiveSeconds(msg.Bubble.Duration); ok {
		details["duration_seconds"] = strconv.Itoa(seconds)
	} else if seconds, ok := parseClockDuration(durationText); ok {
		details["duration_seconds"] = strconv.Itoa(seconds)
	}

	return normalizedContent{Type: "voip", Text: bracketText("语音通话", callText), Details: detailsOrNil(details)}
}

func normalizeVoice(raw string) normalizedContent {
	msg, ok := parseMedia(raw)
	if !ok {
		return normalizedContent{Type: "voice", Text: "[语音]"}
	}
	details := map[string]string{}
	putIfNonZero(details, "voice_length_ms", msg.Voice.VoiceLength)
	putIfNonZero(details, "length", msg.Voice.Length)
	putIfNotEmpty(details, "format", msg.Voice.Format)
	if ms := parsePositiveIntString(msg.Voice.VoiceLength); ms > 0 {
		details["duration_seconds"] = strconv.FormatInt((ms+999)/1000, 10)
	}
	return normalizedContent{Type: "voice", Text: "[语音]", Details: detailsOrNil(details)}
}

func normalizeCard(raw string) normalizedContent {
	msg, ok := parseMedia(raw)
	if !ok {
		return normalizedContent{Type: "card", Text: "[名片]"}
	}
	details := map[string]string{}
	putIfNotEmpty(details, "username", msg.Username)
	putIfNotEmpty(details, "nickname", msg.Nickname)
	putIfNotEmpty(details, "alias", msg.Alias)
	putIfNotEmpty(details, "province", msg.Province)
	putIfNotEmpty(details, "city", msg.City)
	putIfNotEmpty(details, "sex", msg.Sex)
	putIfNotEmpty(details, "small_head_url", msg.SmallImg)
	putIfNotEmpty(details, "big_head_url", msg.BigImg)
	return normalizedContent{Type: "card", Text: bracketText("名片", firstNonEmpty(msg.Nickname, msg.Alias, msg.Username)), Details: detailsOrNil(details)}
}

func normalizeEmoji(raw string) normalizedContent {
	msg, ok := parseMedia(raw)
	if !ok {
		return normalizedContent{Type: "emoji", Text: "[动画表情]"}
	}
	details := map[string]string{}
	putIfNotEmpty(details, "url", msg.Emoji.CdnURL)
	putIfNotEmpty(details, "md5", msg.Emoji.MD5)
	putIfNonZero(details, "length", msg.Emoji.Len)
	putIfNonZero(details, "width", msg.Emoji.Width)
	putIfNonZero(details, "height", msg.Emoji.Height)
	putIfNotEmpty(details, "emoji_type", msg.Emoji.Type)
	putIfNotEmpty(details, "product_id", msg.Emoji.ProductID)
	return normalizedContent{Type: "emoji", Text: "[动画表情]", Details: detailsOrNil(details)}
}

func normalizeImage(raw string) normalizedContent {
	msg, ok := parseMedia(raw)
	if !ok {
		return normalizedContent{Type: "image", Text: "[图片]"}
	}
	img := msg.Image
	details := map[string]string{}
	putIfNotEmpty(details, "md5", img.MD5)
	putIfNotEmpty(details, "origin_md5", img.OriginMD5)
	putIfNonZero(details, "length", img.Length)
	putIfNotEmpty(details, "cdn_thumb_url", img.CDNThumbURL)
	putIfNonZero(details, "cdn_thumb_length", img.CDNThumbLength)
	putIfNonZero(details, "cdn_thumb_width", img.CDNThumbWidth)
	putIfNonZero(details, "cdn_thumb_height", img.CDNThumbHeight)
	putIfNotEmpty(details, "cdn_mid_img_url", img.CDNMidImgURL)
	putIfNonZero(details, "cdn_mid_width", img.CDNMidWidth)
	putIfNonZero(details, "cdn_mid_height", img.CDNMidHeight)
	putIfNonZero(details, "cdn_hd_width", img.CDNHDWidth)
	putIfNonZero(details, "cdn_hd_height", img.CDNHDHeight)
	putIfNonZero(details, "hevc_mid_size", img.HEVCMidSize)
	return normalizedContent{Type: "image", Text: "[图片]", Details: detailsOrNil(details)}
}

func normalizeVideo(raw string) normalizedContent {
	msg, ok := parseMedia(raw)
	if !ok {
		return normalizedContent{Type: "video", Text: "[视频]"}
	}
	video := msg.Video
	details := map[string]string{}
	putIfNotEmpty(details, "md5", video.MD5)
	putIfNotEmpty(details, "new_md5", video.NewMD5)
	putIfNotEmpty(details, "raw_md5", video.RawMD5)
	putIfNotEmpty(details, "origin_source_md5", video.OriginSourceMD5)
	putIfNonZero(details, "length", video.Length)
	putIfNonZero(details, "raw_length", video.RawLength)
	putIfNonZero(details, "play_length", video.PlayLength)
	putIfNotEmpty(details, "from_username", video.FromUsername)
	putIfNotEmpty(details, "cdn_video_url", video.CDNVideoURL)
	putIfNotEmpty(details, "cdn_raw_video_url", video.CDNRawVideoURL)
	putIfNotEmpty(details, "cdn_thumb_url", video.CDNThumbURL)
	putIfNonZero(details, "cdn_thumb_length", video.CDNThumbLength)
	putIfNonZero(details, "cdn_thumb_width", video.CDNThumbWidth)
	putIfNonZero(details, "cdn_thumb_height", video.CDNThumbHeight)
	putIfNonZero(details, "is_placeholder", video.IsPlaceholder)
	putIfNonZero(details, "is_ad", video.IsAd)
	return normalizedContent{Type: "video", Text: "[视频]", Details: detailsOrNil(details)}
}

func normalizeLocation(raw string) normalizedContent {
	msg, ok := parseMedia(raw)
	if !ok {
		return normalizedContent{Type: "location", Text: "[位置]"}
	}
	parts := compactStrings(msg.Location.Label, msg.Location.CityName, msg.Location.X, msg.Location.Y)
	details := map[string]string{}
	putIfNotEmpty(details, "label", msg.Location.Label)
	putIfNotEmpty(details, "city", msg.Location.CityName)
	putIfNotEmpty(details, "x", msg.Location.X)
	putIfNotEmpty(details, "y", msg.Location.Y)
	putIfNotEmpty(details, "map_url", mapURL(msg.Location.X, msg.Location.Y, msg.Location.Label))
	if len(parts) == 0 {
		return normalizedContent{Type: "location", Text: "[位置]", Details: detailsOrNil(details)}
	}
	return normalizedContent{Type: "location", Text: fmt.Sprintf("[位置|%s]", strings.Join(parts, "|")), Details: detailsOrNil(details)}
}

func normalizeSystem(raw string) normalizedContent {
	text := strings.TrimSpace(raw)
	details := map[string]string{}
	if strings.Contains(text, "撤回") {
		details["event"] = "revoke"
	} else if strings.Contains(text, "群公告") {
		details["event"] = "room_announcement"
	} else if strings.Contains(text, "邀请") || strings.Contains(text, "加入了群聊") {
		details["event"] = "room_invite"
	} else if strings.Contains(text, "移出") || strings.Contains(text, "移出了群聊") {
		details["event"] = "room_remove"
	} else if strings.Contains(text, "修改群名") || strings.Contains(text, "群聊名称") {
		details["event"] = "room_rename"
	}
	var sys sysMsg
	if err := xml.Unmarshal([]byte(text), &sys); err == nil {
		putIfNotEmpty(details, "sysmsg_type", sys.Type)
		if sys.Type == "revokemsg" || strings.TrimSpace(sys.Revoke.Content) != "" {
			details["event"] = "revoke"
			putIfNotEmpty(details, "revoke_text", sys.Revoke.Content)
			putIfNotEmpty(details, "revoke_time", sys.Revoke.RevokeTime)
			putIfNotEmpty(details, "revoke_actor", revokeActor(sys.Revoke.Content))
			if strings.TrimSpace(sys.Revoke.Content) != "" {
				text = strings.TrimSpace(sys.Revoke.Content)
			}
		}
		if strings.TrimSpace(sys.Content) != "" {
			text = strings.TrimSpace(sys.Content)
		}
	}
	return normalizedContent{Type: "system", Text: text, Details: detailsOrNil(details)}
}

func normalizeShare(subType int64, raw string) normalizedContent {
	msg, ok := parseMedia(raw)
	if !ok {
		return normalizedContent{Type: "share", Text: strings.TrimSpace(raw)}
	}
	app := msg.App
	appType := int64(app.Type)
	if appType == 0 {
		appType = subType
	}
	details := map[string]string{"app_type": fmt.Sprintf("%d", appType)}
	putIfNotEmpty(details, "title", strings.TrimSpace(app.Title))
	putIfNotEmpty(details, "desc", strings.TrimSpace(app.Des))
	putIfNotEmpty(details, "url", strings.TrimSpace(app.URL))
	putIfNotEmpty(details, "low_url", strings.TrimSpace(app.LowURL))
	putIfNotEmpty(details, "data_url", strings.TrimSpace(app.DataURL))
	putIfNotEmpty(details, "low_data_url", strings.TrimSpace(app.LowDataURL))
	putIfNotEmpty(details, "thumb_url", strings.TrimSpace(app.ThumbURL))
	putIfNotEmpty(details, "md5", strings.TrimSpace(app.MD5))
	putIfNotEmpty(details, "source_username", strings.TrimSpace(app.SourceUsername))
	putIfNotEmpty(details, "source_display_name", strings.TrimSpace(app.SourceDisplayName))
	if app.AppAttach != nil {
		putIfNotEmpty(details, "file_ext", app.AppAttach.FileExt)
		putIfNotEmpty(details, "total_len", app.AppAttach.TotalLen)
		putIfNotEmpty(details, "cdn_attach_url", app.AppAttach.CDNAttachURL)
	}

	switch appType {
	case messageSubTypeText, messageSubTypeLink, messageSubTypeLink2:
		return normalizedContent{Type: "link", Text: linkText(app.Title, app.Des, app.URL), Details: detailsOrNil(details)}
	case messageSubTypeFile:
		return normalizedContent{Type: "file", Text: bracketText("文件", app.Title), Details: detailsOrNil(details)}
	case messageSubTypeGIF:
		if app.URL != "" {
			putIfNotEmpty(details, "url", app.URL)
		} else if msg.Emoji.CdnURL != "" {
			putIfNotEmpty(details, "url", msg.Emoji.CdnURL)
		}
		putIfNotEmpty(details, "emoji_md5", msg.Emoji.MD5)
		putIfNonZero(details, "width", msg.Emoji.Width)
		putIfNonZero(details, "height", msg.Emoji.Height)
		putIfNonZero(details, "length", msg.Emoji.Len)
		return normalizedContent{Type: "gif", Text: "[GIF表情]", Details: detailsOrNil(details)}
	case messageSubTypeChatRecord:
		return normalizeChatRecord(app, details)
	case messageSubTypeMiniProgram, messageSubTypeMiniProgram2:
		title := firstNonEmpty(app.SourceDisplayName, app.Title)
		addWeAppDetails(details, app.WeAppInfo)
		return normalizedContent{Type: "mini_program", Text: linkLikeText("小程序", title, app.URL), Details: detailsOrNil(details)}
	case messageSubTypeChannel:
		if app.FinderFeed != nil {
			title := strings.TrimSpace(strings.ReplaceAll(app.FinderFeed.Desc, "\n", " "))
			url := ""
			if len(app.FinderFeed.MediaList.Media) > 0 {
				url = app.FinderFeed.MediaList.Media[0].URL
			}
			putIfNotEmpty(details, "title", title)
			putIfNotEmpty(details, "url", url)
			putIfNotEmpty(details, "nickname", app.FinderFeed.Nickname)
			putIfNotEmpty(details, "cover_url", url)
			return normalizedContent{Type: "channel", Text: linkLikeText("视频号", title, url), Details: detailsOrNil(details)}
		}
		return normalizedContent{Type: "channel", Text: bracketText("视频号", app.Title), Details: detailsOrNil(details)}
	case messageSubTypeQuote:
		addQuoteReferDetails(details, app.Refer)
		return normalizedContent{Type: "quote", Text: quoteText(app.Title, details["refer_text"]), Details: detailsOrNil(details)}
	case messageSubTypePat:
		return normalizedContent{Type: "pat", Text: firstNonEmpty(app.Title, "[拍一拍]"), Details: detailsOrNil(details)}
	case messageSubTypeChannelLive:
		title := app.Title
		if app.FinderLive != nil && app.FinderLive.Desc != "" {
			title = app.FinderLive.Desc
		}
		putIfNotEmpty(details, "live_desc", title)
		return normalizedContent{Type: "channel_live", Text: bracketText("视频号直播", title), Details: detailsOrNil(details)}
	case messageSubTypeMusic:
		return normalizedContent{Type: "music", Text: linkLikeText("音乐", app.Title, app.URL), Details: detailsOrNil(details)}
	case messageSubTypePay:
		addPayDetails(details, app.WCPayInfo)
		return normalizedContent{Type: "pay", Text: payText(app.WCPayInfo), Details: detailsOrNil(details)}
	case messageSubTypeRedEnvelope:
		putIfNotEmpty(details, "red_envelope_title", app.Title)
		return normalizedContent{Type: "red_envelope", Text: "[红包]", Details: detailsOrNil(details)}
	case messageSubTypeRedEnvelopeCover:
		putIfNotEmpty(details, "red_envelope_title", app.Title)
		return normalizedContent{Type: "red_envelope_cover", Text: "[红包封面]", Details: detailsOrNil(details)}
	default:
		return normalizedContent{Type: "share", Text: bracketText("分享", app.Title), Details: detailsOrNil(details)}
	}
}

func normalizeChatRecord(app appMsg, details map[string]string) normalizedContent {
	info, ok := parseRecordInfo(app.RecordItem)
	title := firstNonEmpty(app.Title, info.Title)
	recordText := strings.TrimSpace(app.Des)
	if ok {
		putIfNotEmpty(details, "record_title", info.Title)
		putIfNotEmpty(details, "record_desc", info.Desc)
		putIfNotEmpty(details, "record_count", recordCountText(info))
		lines := recordItemLines(info.DataList.Items)
		if len(lines) > 0 {
			recordText = strings.Join(lines, "\n")
			putIfNotEmpty(details, "record_text", recordText)
		}
		if itemsJSON := recordItemsJSON(info.DataList.Items); itemsJSON != "" {
			details["record_items_json"] = itemsJSON
		}
	}
	if details["record_count"] == "" && strings.TrimSpace(app.Des) != "" {
		putIfNotEmpty(details, "record_text", strings.TrimSpace(app.Des))
	}
	return normalizedContent{Type: "chat_record", Text: chatRecordText(title, recordText), Details: detailsOrNil(details)}
}

func addWeAppDetails(details map[string]string, info *weAppInfo) {
	if info == nil {
		return
	}
	putIfNotEmpty(details, "app_username", info.Username)
	putIfNotEmpty(details, "app_id", info.AppID)
	putIfNotEmpty(details, "page_path", info.PagePath)
	putIfNotEmpty(details, "app_name", info.SourceDisplayName)
	putIfNotEmpty(details, "appbrand_type", info.Type)
	putIfNotEmpty(details, "pkg_type", info.PkgType)
	putIfNotEmpty(details, "version", info.Version)
	putIfNotEmpty(details, "icon_url", info.IconURL)
}

func addPayDetails(details map[string]string, info *wcPayInfo) {
	if info == nil {
		return
	}
	if info.PaySubType != 0 {
		details["pay_subtype"] = strconv.Itoa(info.PaySubType)
	}
	putIfNotEmpty(details, "fee_desc", info.FeeDesc)
	putIfNotEmpty(details, "pay_memo", info.PayMemo)
}

func addQuoteReferDetails(details map[string]string, ref *referMsg) {
	if ref == nil {
		return
	}
	putIfNotEmpty(details, "refer_chat_username", ref.ChatUser)
	putIfNotEmpty(details, "refer_from_username", ref.FromUser)
	putIfNotEmpty(details, "refer_display_name", ref.DisplayName)
	putIfNotEmpty(details, "refer_create_time", ref.CreateTime)
	putIfNotEmpty(details, "refer_server_id", ref.ServerID)
	putIfNotEmpty(details, "refer_type", ref.Type)

	normalized, ok := normalizeReferContent(ref.Type, ref.Content)
	if !ok {
		return
	}
	putIfNotEmpty(details, "refer_content_type", normalized.Type)
	putIfNotEmpty(details, "refer_text", normalized.Text)
	for key, value := range normalized.Details {
		if key == "app_type" {
			putIfNotEmpty(details, "refer_app_type", value)
			continue
		}
		putIfNotEmpty(details, "refer_"+key, value)
	}
}

func normalizeReferContent(rawType string, rawContent string) (normalizedContent, bool) {
	rawContent = strings.TrimSpace(rawContent)
	if rawContent == "" {
		return normalizedContent{}, false
	}
	msgType, err := strconv.ParseInt(strings.TrimSpace(rawType), 10, 64)
	if err != nil {
		return normalizedContent{Type: "unknown", Text: rawContent}, true
	}
	return normalizeContent(msgType, 0, rawContent), true
}

func parseRecordInfo(raw string) (recordInfo, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return recordInfo{}, false
	}
	var info recordInfo
	if err := xml.Unmarshal([]byte(raw), &info); err != nil {
		return recordInfo{}, false
	}
	if strings.TrimSpace(info.Title) == "" && strings.TrimSpace(info.Desc) == "" && len(info.DataList.Items) == 0 {
		return recordInfo{}, false
	}
	return info, true
}

func recordCountText(info recordInfo) string {
	count := strings.TrimSpace(info.DataList.Count)
	if count != "" {
		return count
	}
	if len(info.DataList.Items) == 0 {
		return ""
	}
	return strconv.Itoa(len(info.DataList.Items))
}

func recordItemLines(items []recordDataItem) []string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		body := recordItemText(item)
		if body == "" {
			continue
		}
		name := strings.TrimSpace(item.SourceName)
		if name != "" {
			lines = append(lines, name+": "+body)
			continue
		}
		lines = append(lines, body)
	}
	return lines
}

func recordItemText(item recordDataItem) string {
	title := strings.TrimSpace(item.DataTitle)
	desc := strings.TrimSpace(item.DataDesc)
	switch recordItemType(item) {
	case "text":
		return firstNonEmpty(desc, title)
	case "mini_program":
		return bracketText("小程序", firstNonEmpty(title, item.AppBrand.SourceDisplayName))
	case "file":
		return bracketText("文件", title)
	case "image":
		return bracketText("图片", title)
	case "video":
		return bracketText("视频", title)
	case "link":
		return bracketText("链接", firstNonEmpty(title, desc))
	default:
		return firstNonEmpty(title, desc)
	}
}

func recordItemsJSON(items []recordDataItem) string {
	if len(items) == 0 {
		return ""
	}
	out := make([]recordItemDetail, 0, len(items))
	for _, item := range items {
		out = append(out, recordItemDetail{
			Type:             recordItemType(item),
			DataType:         strings.TrimSpace(item.DataType),
			DataID:           strings.TrimSpace(item.DataID),
			SourceName:       strings.TrimSpace(item.SourceName),
			SourceTime:       strings.TrimSpace(item.SourceTime),
			Title:            strings.TrimSpace(item.DataTitle),
			Desc:             strings.TrimSpace(item.DataDesc),
			ThumbURL:         strings.TrimSpace(item.CDNThumbURL),
			ThumbMD5:         strings.TrimSpace(item.ThumbFullMD5),
			MD5:              strings.TrimSpace(firstNonEmpty(item.FullMD5, item.DataID)),
			Size:             strings.TrimSpace(firstNonEmpty(item.DataSize, item.ThumbSize)),
			AppName:          strings.TrimSpace(item.AppBrand.SourceDisplayName),
			AppID:            strings.TrimSpace(item.AppBrand.AppID),
			AppUsername:      strings.TrimSpace(item.AppBrand.Username),
			SrcMsgLocalID:    strings.TrimSpace(item.SrcMsgLocalID),
			SrcMsgCreateTime: strings.TrimSpace(item.SrcMsgCreateTime),
		})
	}
	data, err := json.Marshal(out)
	if err != nil {
		return ""
	}
	return string(data)
}

func recordItemType(item recordDataItem) string {
	switch strings.TrimSpace(item.DataType) {
	case "1":
		return "text"
	case "3":
		return "image"
	case "4", "5":
		return "link"
	case "6":
		return "file"
	case "19":
		if strings.TrimSpace(item.AppBrand.AppID) != "" || strings.TrimSpace(item.AppBrand.Username) != "" || strings.TrimSpace(item.AppBrand.SourceDisplayName) != "" {
			return "mini_program"
		}
		return "appmsg"
	case "43":
		return "video"
	default:
		return "unknown"
	}
}

func parseMedia(raw string) (mediaMsg, bool) {
	var msg mediaMsg
	if err := xml.Unmarshal([]byte(raw), &msg); err != nil {
		return mediaMsg{}, false
	}
	return msg, true
}

func parseVOIP(raw string) (voipMsg, bool) {
	var msg voipMsg
	if err := xml.Unmarshal([]byte(raw), &msg); err != nil {
		return voipMsg{}, false
	}
	if strings.TrimSpace(msg.Bubble.Msg) == "" && strings.TrimSpace(msg.Type) == "" && strings.TrimSpace(msg.Bubble.MsgType) == "" {
		return voipMsg{}, false
	}
	return msg, true
}

func linkText(title string, desc string, rawURL string) string {
	title = firstNonEmpty(strings.TrimSpace(title), "链接")
	desc = strings.TrimSpace(desc)
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" && strings.HasPrefix(desc, "http") {
		rawURL = desc
	}
	text := linkLikeText("链接", title, rawURL)
	if desc != "" && desc != rawURL {
		text += "\n" + desc
	}
	return text
}

func linkLikeText(kind string, title string, rawURL string) string {
	title = strings.TrimSpace(title)
	rawURL = strings.TrimSpace(rawURL)
	if title == "" {
		return "[" + kind + "]"
	}
	if rawURL == "" {
		return bracketText(kind, title)
	}
	return fmt.Sprintf("[%s|%s](%s)", kind, title, rawURL)
}

func bracketText(kind string, title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return "[" + kind + "]"
	}
	return fmt.Sprintf("[%s|%s]", kind, title)
}

func quoteText(title string, referText ...string) string {
	title = strings.TrimSpace(title)
	text := "[引用]"
	if title != "" {
		text = "> [引用]\n" + title
	}
	refer := firstNonEmpty(referText...)
	if refer != "" {
		text += "\n> [原消息]\n" + refer
	}
	return text
}

func chatRecordText(title string, recordText string) string {
	text := bracketText("聊天记录", title)
	recordText = strings.TrimSpace(recordText)
	if recordText != "" {
		text += "\n" + recordText
	}
	return text
}

func payText(info *wcPayInfo) string {
	if info == nil {
		return "[转账]"
	}
	action := ""
	switch info.PaySubType {
	case 1, 7:
		action = "发送 "
	case 3, 5:
		action = "接收 "
	case 4:
		action = "退还 "
	}
	memo := ""
	if info.PayMemo != "" {
		memo = "(" + info.PayMemo + ")"
	}
	if info.FeeDesc == "" {
		return "[转账]"
	}
	return fmt.Sprintf("[转账|%s%s]%s", action, info.FeeDesc, memo)
}

func mapURL(x string, y string, label string) string {
	x = strings.TrimSpace(x)
	y = strings.TrimSpace(y)
	if x == "" || y == "" {
		return ""
	}
	return fmt.Sprintf("https://maps.google.com/?q=%s,%s", x, y)
}

func revokeActor(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	for _, marker := range []string{" 撤回了一条消息", "撤回了一条消息"} {
		if before, ok := strings.CutSuffix(content, marker); ok {
			return strings.Trim(strings.TrimSpace(before), `"`)
		}
	}
	return ""
}

func parsePositiveIntString(raw string) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0
	}
	return value
}

func parsePositiveSeconds(raw string) (int, bool) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return 0, false
	}
	return value, true
}

func parseClockDuration(raw string) (int, bool) {
	if raw == "" {
		return 0, false
	}
	parts := strings.Split(raw, ":")
	if len(parts) != 2 && len(parts) != 3 {
		return 0, false
	}
	values := make([]int, 0, len(parts))
	for _, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return 0, false
		}
		values = append(values, value)
	}
	if len(values) == 2 {
		return values[0]*60 + values[1], true
	}
	return values[0]*3600 + values[1]*60 + values[2], true
}

func compactStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func putIfNotEmpty(m map[string]string, key string, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		m[key] = value
	}
}

func putIfNonZero(m map[string]string, key string, value string) {
	value = strings.TrimSpace(value)
	if value != "" && value != "0" {
		m[key] = value
	}
}

func detailsOrNil(details map[string]string) map[string]string {
	if len(details) == 0 {
		return nil
	}
	return details
}
