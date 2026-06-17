// Package engine detects the target database engine from liquibase.properties
// and resolves the appropriate DatabaseProvider.
package engine

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
)

// Engine is a typed string identifying the target database engine.
type Engine string

const (
	EnginePostgres  Engine = "postgres"
	EngineOracle    Engine = "oracle"
	EngineSnowflake Engine = "snowflake"
	EngineUnknown   Engine = "unknown"
)

// Detect reads the liquibase.properties file at propsPath and determines the
// target engine from both the url scheme and the driver class.
//
// Decision table:
//   - pg url + pg driver  -> Postgres (ok)
//   - oracle url (any driver) -> ErrUnsupportedEngine (Oracle)  — unless ambiguous case below
//   - snowflake url       -> ErrUnsupportedEngine (Snowflake, cloud-only)
//   - placeholder url + pg driver -> Postgres (multi-engine scaffold pattern)
//   - oracle url + pg driver -> HARD REJECT (ambiguous — never guess)
//   - unknown              -> ErrUnsupportedEngine
func Detect(propsPath string) (Engine, error) {
	props, err := readRawProps(propsPath)
	if err != nil {
		return EngineUnknown, err
	}

	url := strings.ToLower(props["url"])
	driver := strings.ToLower(props["driver"])

	urlEngine := classifyURL(url)
	driverEngine := classifyDriver(driver)

	// Ambiguous: oracle url but postgres driver — HARD REJECT
	if urlEngine == EngineOracle && driverEngine == EnginePostgres {
		return EngineUnknown, fmt.Errorf("%w: ambiguous configuration — url points to Oracle but driver is PostgreSQL; review liquibase.properties",
			domain.ErrUnsupportedEngine)
	}

	// Snowflake: cloud-only, cannot run ephemerally
	if urlEngine == EngineSnowflake || driverEngine == EngineSnowflake {
		return EngineUnknown, fmt.Errorf("%w: Snowflake is cloud-only and cannot run ephemerally in v1",
			domain.ErrUnsupportedEngine)
	}

	// Oracle
	if urlEngine == EngineOracle || driverEngine == EngineOracle {
		return EngineUnknown, fmt.Errorf("%w: Oracle is out of v1 scope; only PostgreSQL is supported",
			domain.ErrUnsupportedEngine)
	}

	// Postgres: either the url or the driver identifies postgres
	if urlEngine == EnginePostgres || driverEngine == EnginePostgres {
		return EnginePostgres, nil
	}

	// Unknown engine
	return EngineUnknown, fmt.Errorf("%w: unrecognised url=%q driver=%q",
		domain.ErrUnsupportedEngine, props["url"], props["driver"])
}

// classifyURL returns the engine implied by a JDBC URL, or EngineUnknown.
func classifyURL(url string) Engine {
	switch {
	case strings.Contains(url, "jdbc:postgresql"):
		return EnginePostgres
	case strings.Contains(url, "jdbc:oracle"):
		return EngineOracle
	case strings.Contains(url, "jdbc:snowflake") || strings.Contains(url, "snowflakecomputing.com"):
		return EngineSnowflake
	default:
		return EngineUnknown
	}
}

// classifyDriver returns the engine implied by a JDBC driver class name, or EngineUnknown.
func classifyDriver(driver string) Engine {
	switch {
	case strings.Contains(driver, "postgresql"):
		return EnginePostgres
	case strings.Contains(driver, "oracle"):
		return EngineOracle
	case strings.Contains(driver, "snowflake"):
		return EngineSnowflake
	default:
		return EngineUnknown
	}
}

// Detector implements domain.EngineDetector by delegating to Detect.
type Detector struct{}

// NewDetector returns a Detector satisfying domain.EngineDetector.
func NewDetector() *Detector { return &Detector{} }

// Detect satisfies domain.EngineDetector.
func (d *Detector) Detect(propsPath string) (string, error) {
	e, err := Detect(propsPath)
	return string(e), err
}

// Ensure Detector satisfies domain.EngineDetector at compile time.
var _ domain.EngineDetector = (*Detector)(nil)

// readRawProps reads a Java .properties file into a map. Only key=value lines
// are returned; comments (#/!) and blank lines are skipped.
func readRawProps(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	result := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		result[key] = val
	}
	return result, scanner.Err()
}
