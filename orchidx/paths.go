package orchidx

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mr-joshcrane/orchid"
)

func ResolvePath(hctx orchid.HandlerContext, rel string) (string, error) {
	if hctx.RootDir == "" {
		return "", fmt.Errorf("orchidx: ResolvePath requires HandlerContext.RootDir to be set (use WithRootDir)")
	}
	joined := filepath.Join(hctx.RootDir, filepath.Clean(rel))
	abs, err := filepath.Abs(joined)
	if err != nil {
		return "", fmt.Errorf("orchidx: ResolvePath: %w", err)
	}
	if !strings.HasPrefix(abs, hctx.RootDir) {
		return "", fmt.Errorf("orchidx: path %q escapes root %q", rel, hctx.RootDir)
	}
	return abs, nil
}
