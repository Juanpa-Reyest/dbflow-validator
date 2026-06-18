package report

import "strings"

// bannerTemplate is the VERBATIM banner art from the locked design (#598).
// The placeholder "v0.1" is replaced at runtime with the real buildVersion.
// IMPORTANT: this raw string literal must NOT be reformatted or realigned.
const bannerTemplate = `══════════════════════════════════════════════════════════════════
   ██████╗ ██████╗ ███████╗██╗      ██████╗ ██╗    ██╗
   ██╔══██╗██╔══██╗██╔════╝██║     ██╔═══██╗██║    ██║
   ██║  ██║██████╔╝█████╗  ██║     ██║   ██║██║ █╗ ██║
   ██║  ██║██╔══██╗██╔══╝  ██║     ██║   ██║██║███╗██║
   ██████╔╝██████╔╝██║     ███████╗╚██████╔╝╚███╔███╔╝
   ╚═════╝ ╚═════╝ ╚═╝     ╚══════╝ ╚═════╝  ╚══╝╚══╝
        V · A · L · I · D · A · T · O · R   v0.1
──────────────────────────────────────────────────────────────────
   Local database-change validation · fail fast before the PR
   zero side-effects
   ✒  Juanpa Reyest · Development Engineer
      ╭───────────╮
      │ ▸ ~/ _     │
      ╰───────────╯
══════════════════════════════════════════════════════════════════
`

// Banner returns the enterprise banner string with the real build version injected.
// If version is empty, it falls back to "dev".
// The returned string ends with a newline.
func Banner(version string) string {
	if version == "" {
		version = "dev"
	}
	return strings.ReplaceAll(bannerTemplate, "v0.1", version)
}
