package buildinfo

var (
	Version = "0.4.0"
	Commit  = ""
)

func String() string {
	if Commit != "" {
		return Version + " (" + Commit + ")"
	}
	return Version
}
