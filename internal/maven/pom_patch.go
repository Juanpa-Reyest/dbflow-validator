package maven

import (
	"fmt"
	"os"
	"strings"
)

// pluginGroupID and pluginArtifactID identify the dbflow Maven plugin in the pom.
const (
	pluginGroupID    = "com.gs.ftt.coe-ds"
	pluginArtifactID = "relational-db-release-manager-plugin"

	// driverGroupID / driverArtifactID are the Maven coordinates of the PostgreSQL
	// JDBC driver that must be injected into the plugin's classloader.
	driverGroupID    = "org.postgresql"
	driverArtifactID = "postgresql"

	// PostgresDriverVersion is the single source of truth for the PostgreSQL JDBC
	// driver version used by this tool.  It is referenced both in the pom injection
	// XML and in the vendored repository path so a version bump updates both.
	// The vendor package's TestVendoredDriverJarExists test asserts that the
	// vendored JAR at mvn-vendor/repository/.../postgresql-<version>.jar exists for
	// this exact version, preventing silent desync.
	PostgresDriverVersion = "42.7.4"
)

// driverDependencyXML is the XML snippet injected inside <plugin><dependencies>.
// It references PostgresDriverVersion so the pom injection and the vendored JAR
// path are always in sync — there is only one place to change when upgrading.
var driverDependencyXML = `          <dependency>
            <groupId>org.postgresql</groupId>
            <artifactId>postgresql</artifactId>
            <version>` + PostgresDriverVersion + `</version>
          </dependency>`

// InjectDriverDependency reads the pom.xml at pomPath, finds the
// relational-db-release-manager-plugin <plugin> element, and injects the
// PostgreSQL JDBC driver as a <dependency> inside it.
//
// Rationale: the plugin uses a shaded fat-jar that bundles Oracle/MySQL/Snowflake
// drivers but NOT PostgreSQL. The Maven plugin classloader is isolated, so the
// driver must be declared inside the <plugin><dependencies> block to be visible
// to the plugin at runtime.
//
// Returns:
//   - (injected string, false, nil) when the driver was injected; injected is the
//     XML snippet added to the pom.
//   - ("", true, nil) when the driver was already present (idempotent no-op) or
//     when the target plugin was not found (pom unchanged).
//   - ("", false, err) on I/O or parse error.
//
// This function:
//   - Is idempotent (no-op if the driver is already present).
//   - Is a no-op when the target plugin is not found in the pom.
//   - Does NOT parse the pom with an XML decoder to avoid reformatting the file;
//     it uses targeted string replacement to preserve the original formatting.
func InjectDriverDependency(pomPath string) (injected string, noOp bool, err error) {
	data, readErr := os.ReadFile(pomPath)
	if readErr != nil {
		return "", false, fmt.Errorf("InjectDriverDependency: read %q: %w", pomPath, readErr)
	}
	original := string(data)
	patched, patchErr := injectDriver(original)
	if patchErr != nil {
		return "", false, fmt.Errorf("InjectDriverDependency: patch %q: %w", pomPath, patchErr)
	}
	// If nothing changed, it was a no-op (already present or plugin not found).
	if patched == original {
		return "", true, nil
	}
	if writeErr := os.WriteFile(pomPath, []byte(patched), 0o644); writeErr != nil {
		return "", false, fmt.Errorf("InjectDriverDependency: write %q: %w", pomPath, writeErr)
	}
	return driverDependencyXML, false, nil
}

// injectDriver performs the string-level injection on the pom XML content.
// Exported for unit testing via internal/maven_test.
func injectDriver(content string) (string, error) {
	// Locate the target plugin block.
	pluginStart, pluginEnd := locatePluginBlock(content)
	if pluginStart < 0 {
		// Plugin not found — leave pom unchanged.
		return content, nil
	}
	pluginBlock := content[pluginStart:pluginEnd]

	// Idempotency: already contains the driver.
	if strings.Contains(pluginBlock, driverGroupID) {
		return content, nil
	}

	var newPluginBlock string
	if depStart := strings.Index(pluginBlock, "<dependencies>"); depStart >= 0 {
		// There is already a <dependencies> block — inject before the first </dependency> closing,
		// which guarantees we add our dep inside the existing <dependencies>.
		depEnd := strings.Index(pluginBlock, "</dependencies>")
		if depEnd < 0 {
			return content, fmt.Errorf("malformed pom: <dependencies> without </dependencies> in plugin block")
		}
		// Insert driver dependency before </dependencies>.
		newPluginBlock = pluginBlock[:depEnd] +
			driverDependencyXML + "\n        " +
			pluginBlock[depEnd:]
	} else {
		// No <dependencies> block yet — add one before </plugin>.
		closingPlugin := "</plugin>"
		idx := strings.LastIndex(pluginBlock, closingPlugin)
		if idx < 0 {
			return content, fmt.Errorf("malformed pom: no </plugin> closing tag found")
		}
		newPluginBlock = pluginBlock[:idx] +
			"        <dependencies>\n" +
			driverDependencyXML + "\n" +
			"        </dependencies>\n        " +
			pluginBlock[idx:]
	}

	return content[:pluginStart] + newPluginBlock + content[pluginEnd:], nil
}

// locatePluginBlock returns the [start, end) byte offsets of the <plugin>...</plugin>
// block that contains both the target groupId and artifactId.
// Returns (-1, -1) if not found.
func locatePluginBlock(content string) (int, int) {
	search := "<plugin>"
	offset := 0
	for {
		start := strings.Index(content[offset:], search)
		if start < 0 {
			return -1, -1
		}
		start += offset

		end := strings.Index(content[start:], "</plugin>")
		if end < 0 {
			return -1, -1
		}
		end = start + end + len("</plugin>")

		block := content[start:end]
		if strings.Contains(block, pluginGroupID) && strings.Contains(block, pluginArtifactID) {
			return start, end
		}
		offset = end
	}
}
