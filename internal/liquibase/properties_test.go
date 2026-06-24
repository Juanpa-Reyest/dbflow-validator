package liquibase_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
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
		changes, err := patcher.Patch(propsPath, coords)
		if err != nil {
			t.Fatalf("Patch: %v", err)
		}
		// AC-14: should return 4 PropChange entries (url, username, password, driver).
		if len(changes) != 4 {
			t.Errorf("expected 4 PropChange entries, got %d: %v", len(changes), changes)
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
		_, err := patcher.Patch("/nonexistent/path/liquibase.properties", liquibase.PatchCoords{})
		if err == nil {
			t.Fatal("Patch() expected error for missing file, got nil")
		}
	})

	t.Run("uses AliasHost:AliasPort for JDBC URL when alias fields are set", func(t *testing.T) {
		dir := t.TempDir()
		propsPath := dir + "/liquibase.properties"

		initialContent := "url=jdbc:postgresql://localhost:5432/olddb\nusername=old\npassword=old\n"
		if err := os.WriteFile(propsPath, []byte(initialContent), 0o600); err != nil {
			t.Fatalf("write initial: %v", err)
		}

		// Coords with both admin (Host:Port) and alias (AliasHost:AliasPort) set.
		// Patcher should use AliasHost:AliasPort for the JDBC URL.
		coords := liquibase.PatchCoords{
			Host:      "127.0.0.1",
			Port:      54321,
			AliasHost: "postgres",
			AliasPort: 5432,
			User:      "lb_scgolfcore",
			Password:  "lb_v4lid4t0r_pass",
			DBName:    "validatordb",
		}
		patcher := liquibase.NewPatcher()
		if _, err := patcher.Patch(propsPath, coords); err != nil {
			t.Fatalf("Patch: %v", err)
		}

		data, err := os.ReadFile(propsPath)
		if err != nil {
			t.Fatalf("read patched: %v", err)
		}
		props, _ := liquibase.Parse(data)
		gotURL := props.Get("url")

		// Must use the alias path, not the host-mapped path.
		wantURL := "jdbc:postgresql://postgres:5432/validatordb"
		if gotURL != wantURL {
			t.Errorf("url = %q, want %q", gotURL, wantURL)
		}
		// Host-mapped address must NOT appear in the URL.
		if bytes.Contains(data, []byte("127.0.0.1")) {
			t.Errorf("patched url contains host-mapped address 127.0.0.1; expected alias: %s", data)
		}
	})

	t.Run("falls back to Host:Port for JDBC URL when AliasHost is empty", func(t *testing.T) {
		dir := t.TempDir()
		propsPath := dir + "/liquibase.properties"

		initialContent := "url=jdbc:postgresql://localhost:5432/olddb\nusername=old\npassword=old\n"
		if err := os.WriteFile(propsPath, []byte(initialContent), 0o600); err != nil {
			t.Fatalf("write initial: %v", err)
		}

		coords := liquibase.PatchCoords{
			Host:     "127.0.0.1",
			Port:     54321,
			User:     "ephemeral_user",
			Password: "ephemeral_pass",
			DBName:   "testdb",
			// AliasHost intentionally empty — should fall back to Host:Port.
		}
		patcher := liquibase.NewPatcher()
		if _, err := patcher.Patch(propsPath, coords); err != nil {
			t.Fatalf("Patch: %v", err)
		}

		data, _ := os.ReadFile(propsPath)
		props, _ := liquibase.Parse(data)
		gotURL := props.Get("url")

		wantURL := "jdbc:postgresql://127.0.0.1:54321/testdb"
		if gotURL != wantURL {
			t.Errorf("fallback url = %q, want %q", gotURL, wantURL)
		}
	})
}

// ---- PropChange return tests (Slice 6c — AC-14, AC-15) ----

// TestPatch_PropChanges_ContainsAllKeys verifies that Patch returns one PropChange
// per key set (url, username, password, driver) with non-empty After values (AC-14).
func TestPatch_PropChanges_ContainsAllKeys(t *testing.T) {
	dir := t.TempDir()
	propsPath := dir + "/liquibase.properties"

	initialContent := "url=jdbc:oracle:thin:@old:1521:XE\ndriver=oracle.jdbc.OracleDriver\nusername=old_user\npassword=old_pass\n"
	if err := os.WriteFile(propsPath, []byte(initialContent), 0o600); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	coords := domain.ContainerCoords{
		Host:     "127.0.0.1",
		Port:     5432,
		User:     "lb_testschema",
		Password: "secret_password",
		DBName:   "testdb",
	}
	patcher := liquibase.NewPatcher()
	changes, err := patcher.Patch(propsPath, coords)
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	// AC-14: must return 4 changes.
	if len(changes) != 4 {
		t.Fatalf("expected 4 PropChange entries, got %d: %v", len(changes), changes)
	}

	// Index by key for easier assertion.
	byKey := make(map[string]domain.PropChange)
	for _, ch := range changes {
		byKey[ch.Key] = ch
	}

	for _, key := range []string{"url", "username", "password", "driver"} {
		ch, ok := byKey[key]
		if !ok {
			t.Errorf("missing PropChange for key %q", key)
			continue
		}
		if ch.After == "" {
			t.Errorf("PropChange[%q].After must not be empty", key)
		}
	}

	// Before values should reflect the original props file.
	if urlCh := byKey["url"]; !strings.Contains(urlCh.Before, "oracle") {
		t.Errorf("url Before should contain original oracle URL; got: %q", urlCh.Before)
	}
	if uCh := byKey["username"]; uCh.Before != "old_user" {
		t.Errorf("username Before should be 'old_user'; got: %q", uCh.Before)
	}
}

// TestPatch_PropChanges_PasswordInRawChanges verifies that the raw PropChange
// from Patch contains the real password (so the caller knows what to scrub).
// AC-15: the CALLER (orchestrator) must scrub it — not the adapter.
func TestPatch_PropChanges_PasswordInRawChanges(t *testing.T) {
	dir := t.TempDir()
	propsPath := dir + "/liquibase.properties"
	if err := os.WriteFile(propsPath, []byte("url=x\nusername=u\npassword=old\ndriver=d\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	const realPass = "super_secret_lb_pass"
	coords := domain.ContainerCoords{
		Host:     "127.0.0.1",
		Port:     5432,
		User:     "lb_test",
		Password: realPass,
		DBName:   "db",
	}
	patcher := liquibase.NewPatcher()
	changes, err := patcher.Patch(propsPath, coords)
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	// The adapter must return the raw password in PropChange.After so the orchestrator can scrub.
	byKey := make(map[string]domain.PropChange)
	for _, ch := range changes {
		byKey[ch.Key] = ch
	}
	pwCh := byKey["password"]
	if pwCh.After != realPass {
		t.Errorf("expected PropChange[password].After = %q (raw); got: %q", realPass, pwCh.After)
	}
	// This test verifies the ADAPTER returns raw; the ORCHESTRATOR test verifies scrubbing.
}
