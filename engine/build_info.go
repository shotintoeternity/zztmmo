package zztgo

import "strings"

// BuildCommit is stamped by the server binary with:
//
//	go build -ldflags "-X github.com/shotintoeternity/zztmmo/engine.BuildCommit=<commit>"
//
// A local `go build` leaves it as dev, which is still a truthful identity.
var BuildCommit = "dev"

func BuildCommitID() string {
	commit := strings.TrimSpace(BuildCommit)
	if commit == "" {
		return "dev"
	}
	return commit
}

func BuildCommitShort() string {
	commit := BuildCommitID()
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}
