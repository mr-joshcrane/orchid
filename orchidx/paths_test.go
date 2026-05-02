package orchidx

import (
	"strings"
	"testing"

	"github.com/mr-joshcrane/orchid"
)

func TestResolvePath(t *testing.T) {
	tests := []struct {
		name    string
		rootDir string
		rel     string
		wantErr string
	}{
		{
			name:    "simple relative path",
			rootDir: "/tmp/project",
			rel:     "src/main.go",
		},
		{
			name:    "dot-dot escape rejected",
			rootDir: "/tmp/project",
			rel:     "../../etc/passwd",
			wantErr: "escapes root",
		},
		{
			name:    "empty RootDir rejected",
			rootDir: "",
			rel:     "anything",
			wantErr: "requires HandlerContext.RootDir",
		},
		{
			name:    "absolute path within root",
			rootDir: "/tmp/project",
			rel:     ".",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hctx := orchid.HandlerContext{RootDir: tt.rootDir}
			result, err := ResolvePath(hctx, tt.rel)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %q, want containing %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.HasPrefix(result, tt.rootDir) {
				t.Fatalf("result %q should start with rootDir %q", result, tt.rootDir)
			}
		})
	}
}
