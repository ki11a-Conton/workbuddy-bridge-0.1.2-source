package main

import (
	"os"
	"regexp"
	"testing"
)

// The version this plugin reports to the host lives in exactly one constant,
// but it is quoted back in every patch-log entry and in every release artifact
// filename. Those three copies drift silently — the host only surfaces the
// constant, while PATCH-NOTES.md keeps naming whatever version the last patch
// claimed. This guard fails the build the moment the constant and the log
// disagree, so a bump cannot land half-applied.
func TestPluginVersionMatchesPatchLog(t *testing.T) {
	semver := regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	if !semver.MatchString(pluginVersion) {
		t.Fatalf("pluginVersion %q is not a semantic version", pluginVersion)
	}

	raw, err := os.ReadFile("PATCH-NOTES.md")
	if err != nil {
		t.Fatalf("read PATCH-NOTES.md: %v", err)
	}
	documented := regexp.MustCompile(`版本(?:仍是)?\s*(\d+\.\d+\.\d+)`).FindAllStringSubmatch(string(raw), -1)
	if len(documented) == 0 {
		t.Fatal("PATCH-NOTES.md documents no version at all")
	}

	latest := documented[len(documented)-1][1]
	if latest != pluginVersion {
		t.Fatalf("pluginVersion %q disagrees with the latest version documented in PATCH-NOTES.md (%q)", pluginVersion, latest)
	}
}
