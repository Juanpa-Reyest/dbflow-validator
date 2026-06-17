package liquibase_test

import (
	"bytes"
	"os"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/liquibase"
)

// pgPropsContent is a postgres-ready properties file with an extra key.
const pgPropsContent = `url=jdbc:postgresql://localhost:5432/mydb
driver=org.postgresql.Driver
username=pguser
password=pgpass
extra.key=should-be-preserved
`

func TestProperties_ParseAndRender(t *testing.T) {
	t.Run("4-key roundtrip preserves all values", func(t *testing.T) {
		input := "url=jdbc:postgresql://localhost:5432/db\ndriver=org.postgresql.Driver\nusername=u\npassword=p\n"
		props, err := liquibase.Parse([]byte(input))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if got := props.Get("url"); got != "jdbc:postgresql://localhost:5432/db" {
			t.Errorf("url: got %q", got)
		}
		if got := props.Get("driver"); got != "org.postgresql.Driver" {
			t.Errorf("driver: got %q", got)
		}
		if got := props.Get("username"); got != "u" {
			t.Errorf("username: got %q", got)
		}
		if got := props.Get("password"); got != "p" {
			t.Errorf("password: got %q", got)
		}
	})

	t.Run("extra keys preserved in Render output (lossless)", func(t *testing.T) {
		props, err := liquibase.Parse([]byte(pgPropsContent))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		rendered := liquibase.Render(props)
		if !bytes.Contains(rendered, []byte("extra.key=should-be-preserved")) {
			t.Errorf("extra.key not found in rendered output:\n%s", rendered)
		}
	})

	t.Run("Render is idempotent (parse->render->parse->render produces same bytes)", func(t *testing.T) {
		props1, err := liquibase.Parse([]byte(pgPropsContent))
		if err != nil {
			t.Fatalf("Parse 1: %v", err)
		}
		out1 := liquibase.Render(props1)

		props2, err := liquibase.Parse(out1)
		if err != nil {
			t.Fatalf("Parse 2: %v", err)
		}
		out2 := liquibase.Render(props2)

		if !bytes.Equal(out1, out2) {
			t.Errorf("Render not idempotent.\nFirst:\n%s\nSecond:\n%s", out1, out2)
		}
	})

	t.Run("Set overwrites existing key value", func(t *testing.T) {
		props, err := liquibase.Parse([]byte("url=old\ndriver=org.postgresql.Driver\n"))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		props.Set("url", "jdbc:postgresql://newhost:5432/newdb")
		if got := props.Get("url"); got != "jdbc:postgresql://newhost:5432/newdb" {
			t.Errorf("Set url: got %q", got)
		}
		rendered := liquibase.Render(props)
		if !bytes.Contains(rendered, []byte("url=jdbc:postgresql://newhost:5432/newdb")) {
			t.Errorf("Set value not in rendered output:\n%s", rendered)
		}
	})
}

func TestPatch(t *testing.T) {
	t.Run("updates properties with ephemeral container coords and preserves extra keys", func(t *testing.T) {
		dir := t.TempDir()
		propsPath := dir + "/liquibase.properties"

		initialContent := "url=jdbc:oracle:thin:@old:1521:XE\ndriver=oracle.jdbc.OracleDriver\nusername=old\npassword=old\nextra.key=keep-me\n"
		if err := os.WriteFile(propsPath, []byte(initialContent), 0o600); err != nil {
			t.Fatalf("write initial: %v", err)
		}

		coords := liquibase.PatchCoords{
			Host:     "127.0.0.1",
			Port:     54321,
			User:     "ephemeral_user",
			Password: "ephemeral_pass",
			DBName:   "validation_db",
		}
		patcher := liquibase.NewPatcher()
		if err := patcher.Patch(propsPath, coords); err != nil {
			t.Fatalf("Patch: %v", err)
		}

		data, err := os.ReadFile(propsPath)
		if err != nil {
			t.Fatalf("read patched file: %v", err)
		}
		props, err := liquibase.Parse(data)
		if err != nil {
			t.Fatalf("parse patched file: %v", err)
		}

		checks := map[string]string{
			"username": "ephemeral_user",
			"password": "ephemeral_pass",
			"driver":   "org.postgresql.Driver",
		}
		for k, want := range checks {
			if got := props.Get(k); got != want {
				t.Errorf("key %q: got %q, want %q", k, got, want)
			}
		}
		if got := props.Get("extra.key"); got != "keep-me" {
			t.Errorf("extra.key not preserved: got %q", got)
		}
		url := props.Get("url")
		if url == "" {
			t.Error("url key missing after patch")
		}
		// Credentials must not appear in serialized output via the rendered file
		// (password was already patched in; that's expected — the test is that it's
		// there and the URL contains the ephemeral host)
		if !bytes.Contains(data, []byte("127.0.0.1")) {
			t.Errorf("patched url does not contain ephemeral host: %s", data)
		}
	})

	t.Run("file absent returns error", func(t *testing.T) {
		patcher := liquibase.NewPatcher()
		err := patcher.Patch("/nonexistent/path/liquibase.properties", liquibase.PatchCoords{})
		if err == nil {
			t.Fatal("Patch() expected error for missing file, got nil")
		}
	})
}
