package container_test

import (
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/container"
)

// TestNewPostgresProvider_Image verifies the provider reports the image it was
// constructed with, and that an empty image falls back to the default.
func TestNewPostgresProvider_Image(t *testing.T) {
	tests := []struct {
		name  string
		image string
		want  string
	}{
		{name: "custom image is preserved", image: "myregistry/postgres-partman:17", want: "myregistry/postgres-partman:17"},
		{name: "explicit default", image: "postgres:17.4", want: "postgres:17.4"},
		{name: "empty falls back to default", image: "", want: "postgres:17.4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := container.NewPostgresProvider(tt.image)
			if got := p.Image(); got != tt.want {
				t.Errorf("Image() = %q, want %q", got, tt.want)
			}
		})
	}
}
