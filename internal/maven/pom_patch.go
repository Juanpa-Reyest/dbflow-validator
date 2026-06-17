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

	// driverGroupID / driverArtifactID / driverVersion are the coordinates of the
	// PostgreSQL JDBC driver that must be injected into the plugin's classloader.
	driverGroupID    = "org.postgresql"
	driverArtifactID = "postgresql"
	driverVersion    = "42.7.4"
)

// driverDependencyXML is the XML snippet injected inside <plugin><dependencies>.
const driverDependencyXML = `          <dependency>
            <groupId>org.postgresql</groupId>
            <artifactId>postgresql</artifactId>
            <version>42.7.4</version>
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
// This function:
//   - Is idempotent (no-op if the driver is already present).
//   - Is a no-op when the target plugin is not found in the pom.
//   - Does NOT parse the pom with an XML decoder to avoid reformatting the file;
//     it uses targeted string replacement to preserve the original formatting.
func InjectDriverDependency(pomPath string) error {
	data, err := os.ReadFile(pomPath)
	if err != nil {
		return fmt.Errorf("InjectDriverDependency: read %q: %w", pomPath, err)
	}
	patched, err := injectDriver(string(data))
	if err != nil {
		return fmt.Errorf("InjectDriverDependency: patch %q: %w", pomPath, err)
	}
	if err := os.WriteFile(pomPath, []byte(patched), 0o644); err != nil {
		return fmt.Errorf("InjectDriverDependency: write %q: %w", pomPath, err)
	}
	return nil
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
