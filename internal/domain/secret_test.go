package domain_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
)

func TestSecret(t *testing.T) {
	const raw = "super-secret-token"
	s := domain.NewSecret(raw)

	tests := []struct {
		name string
		fn   func() string
		want string
	}{
		{
			name: "String() returns redacted",
			fn:   func() string { return fmt.Sprintf("%s", s) },
			want: "***",
		},
		{
			name: "GoString() returns redacted",
			fn:   func() string { return fmt.Sprintf("%#v", s) },
			want: "***",
		},
		{
			name: "Reveal() returns raw value",
			fn:   s.Reveal,
			want: raw,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.fn()
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("MarshalJSON() returns redacted", func(t *testing.T) {
		type wrapper struct {
			Token domain.Secret `json:"token"`
		}
		b, err := json.Marshal(wrapper{Token: s})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		got := string(b)
		want := `{"token":"***"}`
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}
