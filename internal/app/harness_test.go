package app_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hakastein/gospy/internal/app"
	"github.com/hakastein/gospy/internal/config"
	"github.com/hakastein/gospy/internal/procscan"
)

const (
	pyroscopeURL      = "http://pyroscope.test"
	shutdownSafetyNet = 15 * time.Second
	settleTime        = 700 * time.Millisecond
)

type capturedRequest struct {
	method string
	path   string
	query  url.Values
	body   string
}

type captureTransport struct {
	mu       sync.Mutex
	requests []capturedRequest
}

func (transport *captureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}

	transport.mu.Lock()
	transport.requests = append(transport.requests, capturedRequest{
		method: request.Method,
		path:   request.URL.Path,
		query:  request.URL.Query(),
		body:   string(body),
	})
	transport.mu.Unlock()

	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     make(http.Header),
	}, nil
}

func (transport *captureTransport) captured() []capturedRequest {
	transport.mu.Lock()
	defer transport.mu.Unlock()

	return append([]capturedRequest(nil), transport.requests...)
}

// processTable is the Processes seam: a process table the test edits while the run goes on.
type processTable struct {
	mu        sync.Mutex
	processes []procscan.Process
}

func (table *processTable) Scan() ([]procscan.Process, error) {
	table.mu.Lock()
	defer table.mu.Unlock()

	return append([]procscan.Process(nil), table.processes...), nil
}

func (table *processTable) set(processes ...procscan.Process) {
	table.mu.Lock()
	defer table.mu.Unlock()

	table.processes = processes
}

// failingTable fails its first scans, the way a procfs that is briefly unreadable would.
type failingTable struct {
	processTable
	failures atomic.Int32
}

func (table *failingTable) Scan() ([]procscan.Process, error) {
	if table.failures.Add(-1) >= 0 {
		return nil, errors.New("process table unavailable")
	}

	return table.processTable.Scan()
}

func worker(pid int) procscan.Process {
	return procscan.Process{PID: pid, ParentPID: 1, StartTime: uint64(1000 + pid), Comm: "php-fpm", Cmdline: "php-fpm: pool www"}
}

func listener(pid int) procscan.Process {
	return procscan.Process{PID: pid, ParentPID: 1, StartTime: uint64(1000 + pid), Comm: "php", Cmdline: "php console.php queue:listen"}
}

func script(pid int) procscan.Process {
	return procscan.Process{PID: pid, ParentPID: 1, StartTime: uint64(1000 + pid), Comm: "php", Cmdline: "php /srv/app/bin/report.php"}
}

func fixturePath(t *testing.T, fixture string) string {
	t.Helper()

	path, err := filepath.Abs(filepath.Join("testdata", "phpspy", fixture))
	require.NoError(t, err)
	require.FileExists(t, path)

	return path
}

// scriptedPhpspy writes a phpspy stand-in that runs the script for the PID it is given as
// `-p <pid>`; a PID without a script exits 1. Every script sees $MARKER_DIR for its notes.
func scriptedPhpspy(t *testing.T, scripts map[int]string) (executable string, markerDir string) {
	t.Helper()

	dir := t.TempDir()
	markerDir = filepath.Join(dir, "markers")
	require.NoError(t, os.Mkdir(markerDir, 0o755))

	var body strings.Builder
	body.WriteString("#!/bin/sh\nMARKER_DIR=" + markerDir + "\nexport MARKER_DIR\npid=$2\ncase \"$pid\" in\n")
	for pid, script := range scripts {
		fmt.Fprintf(&body, "%d)\n%s\n;;\n", pid, script)
	}
	body.WriteString("*) exit 1;;\nesac\n")

	executable = filepath.Join(dir, "phpspy")
	writeExecutable(t, executable, body.String())

	return executable, markerDir
}

// writeExecutable holds off every fork in the test binary while the file is open for writing:
// a phpspy forked by a parallel test's runtime in that window inherits the descriptor until it
// execs, and an exec of the script meanwhile fails with ETXTBSY.
func writeExecutable(t *testing.T, path, body string) {
	t.Helper()

	syscall.ForkLock.Lock()
	defer syscall.ForkLock.Unlock()

	require.NoError(t, os.WriteFile(path, []byte(body), 0o755))
}

// replayAndHold prints the fixture once and then stays attached until detached.
func replayAndHold(t *testing.T, fixture string) string {
	t.Helper()

	return "echo $$ > \"$MARKER_DIR/pid.$pid\"\ncat " + fixturePath(t, fixture) + "\nexec sleep 60"
}

func loadConfig(t *testing.T, content string) config.Config {
	t.Helper()

	cfg, _, err := config.Load([]byte(content), func(string) (string, bool) { return "", false })
	require.NoError(t, err)

	return cfg
}

// checkoutConfig is the smallest configuration the runtime tests share: two targets, fpm
// workers at 25 Hz with a uri tag and queue listeners at 10 Hz tagged by entry point.
func checkoutConfig(t *testing.T, executable string, extra string) config.Config {
	t.Helper()

	return loadConfig(t, `
pyroscope:
  url: `+pyroscopeURL+`
  workers: 1
  rate-mb: 100
  rate-burst-mb: 100
app: checkout
tags: { env: production }
phpspy: `+executable+`
batch-interval: 100ms
stats-interval: 0
`+extra+`
targets:
  - name: fpm
    match: { comm: php-fpm, cmdline: '^php-fpm: pool ' }
    max-processes: 2
    rate: 25
    scan-interval: 100ms
    tags:
      source: fpm
      uri: '{{ "glopeek server.REQUEST_URI" }}'
  - name: queue
    match: { comm: php, cmdline: 'queue:listen' }
    max-processes: 1
    rate: 10
    scan-interval: 100ms
    tags: { source: queue }
    entrypoints: [index.php]
    tag-entrypoint: true
    keep-entrypoint-name: false
`)
}

func startRun(ctx context.Context, cfg app.Config) <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx, cfg)
	}()

	return done
}

func awaitRun(t *testing.T, done <-chan error) error {
	t.Helper()

	select {
	case err := <-done:
		return err
	case <-time.After(shutdownSafetyNet):
		t.Fatal("Run did not return in time")
		return nil
	}
}

func awaitSignal(t *testing.T, ready <-chan struct{}, failure string) {
	t.Helper()

	select {
	case <-ready:
	case <-time.After(shutdownSafetyNet):
		t.Fatal(failure)
	}
}

func hangingPyroscope(t *testing.T) (string, <-chan struct{}) {
	t.Helper()

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}

		select {
		case <-release:
		case <-request.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })

	return server.URL, started
}

func countStacks(t *testing.T, requests []capturedRequest) map[string]map[string]int {
	t.Helper()

	profiles := make(map[string]map[string]int)
	for _, request := range requests {
		name := request.query.Get("name")
		if profiles[name] == nil {
			profiles[name] = make(map[string]int)
		}

		for _, line := range strings.Split(request.body, "\n") {
			separator := strings.LastIndex(line, " ")
			require.Greater(t, separator, 0, "folded line %q has no count", line)

			count, err := strconv.Atoi(line[separator+1:])
			require.NoError(t, err)
			profiles[name][line[:separator]] += count
		}
	}

	return profiles
}

func sampleRates(requests []capturedRequest) map[string]string {
	rates := make(map[string]string)
	for _, request := range requests {
		rates[request.query.Get("name")] = request.query.Get("sampleRate")
	}

	return rates
}

func profileNames(requests []capturedRequest) []string {
	seen := make(map[string]struct{})
	for _, request := range requests {
		seen[request.query.Get("name")] = struct{}{}
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)

	return names
}

func peekGlobalStacks(name string) map[string]map[string]int {
	return map[string]map[string]int{
		name + "{env=production,source=fpm,uri=/orders/42}": {
			`main /srv/app/public/index.php;App\Kernel::handle;App\Controller\OrderController::show;App\Repository\OrderRepository::find;PDO::prepare`: 2,
			`main /srv/app/public/index.php;App\Kernel::handle;App\Controller\OrderController::show;json_encode`:                                       1,
		},
		name + "{env=production,source=fpm,uri=/cart}": {
			`main /srv/app/public/index.php;App\Kernel::handle;App\Controller\CartController::add;usleep`: 1,
		},
	}
}

func requestInfoStacks(name string) map[string]map[string]int {
	return map[string]map[string]int{
		name + "{env=production,source=queue,entrypoint=/srv/app/public/index.php}": {
			`main;App\Controller\OrderController::show;App\Repository\OrderRepository::find;mysqli::query`: 2,
		},
	}
}

func merge(profiles ...map[string]map[string]int) map[string]map[string]int {
	merged := make(map[string]map[string]int)
	for _, profile := range profiles {
		for name, stacks := range profile {
			merged[name] = stacks
		}
	}

	return merged
}

// pidOf reads the PID a script wrote with `echo $$ > file` once the line is complete.
func pidOf(t *testing.T, file string) int {
	t.Helper()

	var pid int
	require.Eventually(t, func() bool {
		content, err := os.ReadFile(file)
		if err != nil || !strings.HasSuffix(string(content), "\n") {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(content)))
		return err == nil
	}, shutdownSafetyNet, 10*time.Millisecond, "the script never wrote its pid to %s", file)

	return pid
}

func requireGone(t *testing.T, pid int) {
	t.Helper()

	// A zombie still answers signal 0, so ESRCH means the process is gone for good.
	require.Eventually(t, func() bool {
		return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
	}, shutdownSafetyNet, 10*time.Millisecond, "process %d is still around", pid)
}

func markerLines(t *testing.T, file string) int {
	t.Helper()

	content, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)

	return strings.Count(string(content), "\n")
}
