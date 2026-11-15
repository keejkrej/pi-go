// Ported from packages/coding-agent/src/experimental/vacation/sessions.ts (pi v1.0.0).

package vacation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/lockfile"
)

// SessionLocation identifies one locked session directory holding session.sqlite.
type SessionLocation struct {
	Id        string
	Directory string
	Database  string
	Cwd       string
	Created   bool
	Release   func() error
}

var sessDirPattern = regexp.MustCompile(`^\d{13}-[0-9a-f-]{36}$`)

// SelectSession selects a new session for cwdInput or its newest one when continueSession is true.
func SelectSession(ctx context.Context, cwdInput string, continueSession bool) (*SessionLocation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	abs, err := filepath.Abs(cwdInput)
	if err != nil {
		return nil, err
	}
	cwd, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}

	hashBytes := sha256.Sum256([]byte(cwd))
	cwdHash := hex.EncodeToString(hashBytes[:])[:24]
	root := filepath.Join(utils.GetAgentDir(), "experimental", "vacation-sessions", cwdHash)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}

	var directory string
	created := false
	if continueSession {
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, err
		}
		var matches []string
		for _, entry := range entries {
			if entry.IsDir() && sessDirPattern.MatchString(entry.Name()) {
				matches = append(matches, entry.Name())
			}
		}
		sort.Strings(matches)
		if len(matches) == 0 {
			return nil, fmt.Errorf("No vacation session exists for %s", cwd)
		}
		directory = filepath.Join(root, matches[len(matches)-1])
	} else {
		directory = filepath.Join(root, fmt.Sprintf("%013d-%s", time.Now().UnixMilli(), uuid.NewString()))
		if err := os.Mkdir(directory, 0o755); err != nil {
			return nil, err
		}
		created = true
	}

	// A lock left by a crashed process goes stale after 10 s; wait that long before giving up.
	realpath := false
	retries := 12
	minTimeout := int64(1000)
	maxTimeout := int64(1000)
	release, err := lockfile.Lock(ctx, directory, &lockfile.Options{
		Realpath: &realpath,
		Retries: &lockfile.RetryOptions{
			Retries:    &retries,
			MinTimeout: &minTimeout,
			MaxTimeout: &maxTimeout,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("Session is already open in another process: %s: %w", directory, err)
	}

	return &SessionLocation{
		Id:        filepath.Base(directory),
		Directory: directory,
		Database:  filepath.Join(directory, "session.sqlite"),
		Cwd:       cwd,
		Created:   created,
		Release:   release,
	}, nil
}
