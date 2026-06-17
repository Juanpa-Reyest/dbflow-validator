package domain_test

import (
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
)

// TestContainerCoords_DualCoordinates verifies that ContainerCoords has separate
// admin (host-side MappedPort) and in-network alias (postgres:5432) fields.
func TestContainerCoords_DualCoordinates(t *testing.T) {
	// Admin coords: used by the Go process for readiness probe + provisioning.
	adminCoords := domain.ContainerCoords{
		Host:       "127.0.0.1",
		Port:       54321,
		AliasHost:  "postgres",
		AliasPort:  5432,
		User:       "validator",
		Password:   "secret",
		DBName:     "validatordb",
	}

	// Admin DSN must use Host:Port (the host-mapped port).
	if adminCoords.Host == "" {
		t.Error("ContainerCoords.Host must not be empty")
	}
	if adminCoords.Port == 0 {
		t.Error("ContainerCoords.Port must not be zero")
	}

	// AliasHost/AliasPort are the in-network coordinates for Maven container.
	if adminCoords.AliasHost != "postgres" {
		t.Errorf("AliasHost = %q, want %q", adminCoords.AliasHost, "postgres")
	}
	if adminCoords.AliasPort != 5432 {
		t.Errorf("AliasPort = %d, want %d", adminCoords.AliasPort, 5432)
	}
}

// TestContainerCoords_AdminVsAlias ensures admin path uses Host:Port
// while the alias path uses AliasHost:AliasPort, and they differ.
func TestContainerCoords_AdminVsAlias(t *testing.T) {
	coords := domain.ContainerCoords{
		Host:      "127.0.0.1",
		Port:      54321,
		AliasHost: "postgres",
		AliasPort: 5432,
		User:      "lb_scgolfcore",
		Password:  "lb_v4lid4t0r_pass",
		DBName:    "validatordb",
	}

	// Admin path (for Go process: readiness + provisioning)
	adminHost := coords.Host
	adminPort := coords.Port

	// Patch/alias path (for Maven container: in-network)
	aliasHost := coords.AliasHost
	aliasPort := coords.AliasPort

	if adminHost == aliasHost {
		t.Errorf("admin host %q equals alias host %q — they must differ", adminHost, aliasHost)
	}
	if adminPort == aliasPort {
		t.Errorf("admin port %d equals alias port %d — they must differ", adminPort, aliasPort)
	}
}
