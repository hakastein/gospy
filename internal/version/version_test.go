package version_test

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hakastein/gospy/internal/version"
)

func TestGetNeverReportsRawBuildInfoPlaceholder(t *testing.T) {
	got := version.Get()

	require.NotEmpty(t, got)
	require.NotEqual(t, "(devel)", got, "the build-info placeholder must not leak to users")
}

// The release relies on the linker flag reaching the binary, so the only honest
// check is to build one and ask it.
func TestGetReportsTheLinkerFlag(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}

	goBin, err := exec.LookPath("go")
	require.NoError(t, err, "the Go toolchain is required to build the probe binary")

	const want = "v9.9.9-linker-flag"

	binary := filepath.Join(t.TempDir(), "gospy")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}

	build := exec.Command(
		goBin, "build",
		"-ldflags", "-X github.com/hakastein/gospy/internal/version.version="+want,
		"-o", binary,
		"github.com/hakastein/gospy/cmd/gospy",
	)
	buildOutput, err := build.CombinedOutput()
	require.NoErrorf(t, err, "go build failed: %s", buildOutput)

	reported, err := exec.Command(binary, "--version").CombinedOutput()
	require.NoErrorf(t, err, "gospy --version failed: %s", reported)
	require.Contains(t, string(reported), want)
}
