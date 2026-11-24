package buildinfo

var (
	Version = "0.6.0"
	Commit  = ""
)

func String() string {
	if Commit != "" {
		return Version + " (" + Commit + ")"
	}
	return Version
}
