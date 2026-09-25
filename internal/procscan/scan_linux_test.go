package procscan_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hakastein/gospy/internal/procscan"
)

func fixtureRoot(t *testing.T) string {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("testdata", "proc"))
	require.NoError(t, err)
	require.DirExists(t, root)

	return root
}

func byPID(processes []procscan.Process) map[int]procscan.Process {
	indexed := make(map[int]procscan.Process, len(processes))
	for _, process := range processes {
		indexed[process.PID] = process
	}

	return indexed
}

func TestScanReadsTheProcessTable(t *testing.T) {
	t.Parallel()

	processes, err := procscan.New(fixtureRoot(t), procscan.FieldUID|procscan.FieldExe).Scan()
	require.NoError(t, err)

	require.Equal(t, map[int]procscan.Process{
		1: {
			PID:       1,
			ParentPID: 0,
			StartTime: 5,
			UID:       0,
			Comm:      "systemd",
			Cmdline:   "/sbin/init",
			Exe:       "/usr/lib/systemd/systemd",
		},
		2: {
			PID:       2,
			ParentPID: 0,
			StartTime: 5,
			UID:       0,
			Comm:      "kthreadd",
			Cmdline:   "",
			Exe:       "",
		},
		200: {
			PID:       200,
			ParentPID: 1,
			StartTime: 1500,
			UID:       0,
			Comm:      "php-fpm",
			Cmdline:   "php-fpm: master process (/usr/local/etc/php-fpm.conf)",
			Exe:       "/usr/local/sbin/php-fpm",
		},
		201: {
			PID:       201,
			ParentPID: 200,
			StartTime: 1600,
			UID:       33,
			Comm:      "php-fpm",
			Cmdline:   "php-fpm: pool www",
			Exe:       "/usr/local/sbin/php-fpm",
		},
		300: {
			PID:       300,
			ParentPID: 1,
			StartTime: 2000,
			UID:       33,
			Comm:      "php",
			Cmdline:   "php console.php queue:listen --memory=512",
			Exe:       "/usr/local/bin/php",
		},
		301: {
			PID:       301,
			ParentPID: 300,
			StartTime: 2100,
			UID:       33,
			Comm:      "php (evil) x",
			Cmdline:   "/tmp/php (evil) x",
			Exe:       "",
		},
	}, byPID(processes), "every process directory is read once; vanished and non-process entries are skipped")
}

func TestScanReadsOptionalFieldsOnlyWhenAsked(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		fields  procscan.Fields
		wantUID int
		wantExe string
	}{
		{
			name:    "neither",
			fields:  0,
			wantUID: -1,
			wantExe: "",
		},
		{
			name:    "user id",
			fields:  procscan.FieldUID,
			wantUID: 33,
			wantExe: "",
		},
		{
			name:    "executable",
			fields:  procscan.FieldExe,
			wantUID: -1,
			wantExe: "/usr/local/bin/php",
		},
		{
			name:    "both",
			fields:  procscan.FieldUID | procscan.FieldExe,
			wantUID: 33,
			wantExe: "/usr/local/bin/php",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			processes, err := procscan.New(fixtureRoot(t), tc.fields).Scan()
			require.NoError(t, err)

			process, ok := byPID(processes)[300]
			require.True(t, ok)
			require.Equal(t, tc.wantUID, process.UID)
			require.Equal(t, tc.wantExe, process.Exe)
			require.Equal(t, "php", process.Comm, "the always-read fields do not depend on the selection")
			require.Equal(t, uint64(2000), process.StartTime)
		})
	}
}

func TestScanReportsAMissingRoot(t *testing.T) {
	t.Parallel()

	_, err := procscan.New(filepath.Join(t.TempDir(), "proc"), 0).Scan()
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestScanFindsTheRunningProcessInTheLiveTable(t *testing.T) {
	t.Parallel()

	processes, err := procscan.New("", procscan.FieldUID|procscan.FieldExe).Scan()
	require.NoError(t, err)

	self, ok := byPID(processes)[os.Getpid()]
	require.True(t, ok, "the test process is missing from its own process table")

	comm, err := os.ReadFile("/proc/self/comm")
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(string(comm)), self.Comm)
	require.Equal(t, os.Getppid(), self.ParentPID)
	require.Equal(t, os.Getuid(), self.UID)
	require.Positive(t, self.StartTime)
	require.Contains(t, self.Cmdline, "procscan.test")

	exe, err := os.Executable()
	require.NoError(t, err)
	require.Equal(t, exe, self.Exe)
}
