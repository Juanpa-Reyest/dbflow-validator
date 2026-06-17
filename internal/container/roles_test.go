package container_test

import (
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/container"
)

// TestBuildCreateRolesSQL verifies that BuildCreateRolesSQL generates idempotent
// CREATE ROLE SQL for the given role names.
func TestBuildCreateRolesSQL(t *testing.T) {
	tests := []struct {
		name  string
		roles []string
		want  []string // substrings that must appear in output
	}{
		{
			name:  "single role",
			roles: []string{"appbackend"},
			want:  []string{"CREATE ROLE IF NOT EXISTS appbackend"},
		},
		{
			name:  "multiple roles",
			roles: []string{"appbackend", "readonly"},
			want: []string{
				"CREATE ROLE IF NOT EXISTS appbackend",
				"CREATE ROLE IF NOT EXISTS readonly",
			},
		},
		{
			name:  "empty roles list",
			roles: nil,
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql := container.BuildCreateRolesSQL(tt.roles)
			for _, w := range tt.want {
				found := false
				for _, line := range sql {
					if line == w || containsStr(line, w) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("BuildCreateRolesSQL(%v) missing %q\ngot: %v", tt.roles, w, sql)
				}
			}
		})
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || s[0:len(sub)] == sub || containsInner(s, sub))
}

func containsInner(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
