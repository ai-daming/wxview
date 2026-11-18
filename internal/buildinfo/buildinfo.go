package buildinfo

var (
	Version = "0.5.0"
	Commit  = ""
)

func String() string {
	if Commit != "" {
		return Version + " (" + Commit + ")"
	}
	return Version
}
