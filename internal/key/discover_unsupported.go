//go:build !darwin && !windows

package key

import "fmt"

const contactRelPath = "contact/contact.db"
const sessionRelPath = "session/session.db"
const favoriteRelPath = "favorite/favorite.db"
const snsRelPath = "sns/sns.db"
const headImageRelPath = "head_image/head_image.db"

type TargetDB struct {
	Account   string
	DataDir   string
	DBRelPath string
	DBPath    string
}

func DiscoverContactDB() (TargetDB, error) {
	return TargetDB{}, fmt.Errorf("automatic WeChat discovery is only implemented for macOS or Windows WeChat 4.x in V1")
}

func DiscoverMessageDBs() ([]TargetDB, error) {
	return nil, fmt.Errorf("automatic WeChat discovery is only implemented for macOS or Windows WeChat 4.x in V1")
}

func DiscoverBizMessageDBs() ([]TargetDB, error) {
	return nil, fmt.Errorf("automatic WeChat discovery is only implemented for macOS or Windows WeChat 4.x in V1")
}

func DiscoverMediaDBs() ([]TargetDB, error) {
	return nil, fmt.Errorf("automatic WeChat discovery is only implemented for macOS or Windows WeChat 4.x in V1")
}

func DiscoverMessageRelatedDBs() ([]TargetDB, error) {
	return nil, fmt.Errorf("automatic WeChat discovery is only implemented for macOS or Windows WeChat 4.x in V1")
}

func DiscoverMessageAuxDBs() ([]TargetDB, error) {
	return nil, fmt.Errorf("automatic WeChat discovery is only implemented for macOS or Windows WeChat 4.x in V1")
}

func DiscoverRequiredDBs() ([]TargetDB, error) {
	return nil, fmt.Errorf("automatic WeChat discovery is only implemented for macOS or Windows WeChat 4.x in V1")
}

func DiscoverSupportedDBs() ([]TargetDB, error) {
	return nil, fmt.Errorf("automatic WeChat discovery is only implemented for macOS or Windows WeChat 4.x in V1")
}

func DiscoverFavoriteDB() (TargetDB, bool) {
	return TargetDB{}, false
}

func DiscoverSessionDB() (TargetDB, bool) {
	return TargetDB{}, false
}

func DiscoverSNSDB() (TargetDB, bool) {
	return TargetDB{}, false
}

func DiscoverHeadImageDB() (TargetDB, bool) {
	return TargetDB{}, false
}

func DiscoverWatcherAccount() (TargetDB, error) { return DiscoverContactDB() }
