// Package tests builds overselect and runs it the way users do: on the
// examples, on real rendered charts, and on broken input.
package tests

import (
	"bytes"
	"crypto/rand"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "overselect-test")
	if err != nil {
		panic(err)
	}
	name := "overselect"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary = filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-o", binary, "./plugin/scripts")
	cmd.Dir = ".."
	if out, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(dir)
		panic("go build: " + err.Error() + "\n" + string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// run runs overselect from the repo root, with stdin if given.
func run(t *testing.T, stdin []byte, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = ".."
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return out.String(), errb.String(), code
}

func read(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", p))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

const clean = "Result: every selector checked matches the pods of one workload"

func TestExamples(t *testing.T) {
	out, _, code := run(t, nil, "examples/shared-labels")
	for _, want := range []string{"ERROR OS101", "ERROR OS201", "ERROR OS301", "WARN OS202", "doesn't declare 8080", "add app.kubernetes.io/component=server to its selector", "HorizontalPodAutoscaler demo-api scales"} {
		if !strings.Contains(out, want) {
			t.Errorf("shared-labels: missing %q\n%s", want, out)
		}
	}
	if code != 1 {
		t.Errorf("shared-labels: exit %d", code)
	}
	for _, failOn := range []string{"warn", "error"} {
		if _, _, code := run(t, nil, "--fail-on", failOn, "examples/shared-labels"); code != 1 {
			t.Errorf("shared-labels --fail-on %s: exit %d", failOn, code)
		}
	}
	if _, _, code := run(t, nil, "--fail-on", "never", "examples/shared-labels"); code != 0 {
		t.Errorf("shared-labels --fail-on never: exit %d", code)
	}
	// The healthy example must stay quiet.
	out, _, code = run(t, nil, "examples/healthy")
	if code != 0 || !strings.Contains(out, clean) || strings.Contains(out, " OS") {
		t.Errorf("healthy: exit %d\n%s", code, out)
	}
	out, _, code = run(t, nil, "--format", "json", "examples/healthy")
	if code != 0 || !strings.Contains(out, `"findings": []`) {
		t.Errorf("healthy json: exit %d\n%s", code, out)
	}
}

// TestRealCharts runs on helm template output of public charts (see
// testdata/NOTICE.md).
func TestRealCharts(t *testing.T) {
	out, _, code := run(t, nil, "tests/testdata/apexkube-agent-2.2.0.yaml")
	if code != 1 || !strings.Contains(out, "ERROR OS301") || !strings.Contains(out, "spec.selector of Deployment apexkube-agent also matches the pods of Deployment apexkube-agent-metrics") {
		t.Errorf("apexkube-agent 2.2.0: exit %d\n%s", code, out)
	}
	for _, f := range []string{"tests/testdata/podinfo.yaml", "tests/testdata/cert-manager.yaml"} {
		out, _, code := run(t, nil, f)
		if code != 0 || !strings.Contains(out, clean) || strings.Contains(out, " OS") {
			t.Errorf("%s: exit %d\n%s", f, code, out)
		}
	}
	// helm template | overselect -
	out, _, code = run(t, read(t, "tests/testdata/apexkube-agent-2.2.0.yaml"), "-")
	if code != 1 || !strings.Contains(out, "stdin:") {
		t.Errorf("stdin: exit %d\n%s", code, out)
	}
}

// broken returns files that must never give a result: empty, binary
// garbage, text garbage, cut off, and the output of failed commands.
func broken(t *testing.T, real []byte) map[string][]byte {
	junk := make([]byte, 512)
	if _, err := rand.Read(junk); err != nil {
		t.Fatal(err)
	}
	junk[0] = 0
	return map[string][]byte{
		"empty":         {},
		"whitespace":    []byte("\n\n  \n"),
		"binary":        junk,
		"text":          []byte("lorem ipsum dolor sit amet\n"),
		"yaml-scalar":   []byte("just a string\n"),
		"cut-half":      real[:len(real)/2],
		"cut-nolast":    real[:len(real)-1],
		"helm-failed":   []byte("Error: INSTALLATION FAILED: template: api/templates/service.yaml:9:20: executing \"api/templates/service.yaml\" at <.Values.x>: nil pointer\n"),
		"helm-render":   []byte("Error: execution error at (api/templates/deployment.yaml:30:28): image.tag is required\n\nUse --debug flag to render out invalid YAML\n"),
		"kubectl":       []byte("error: error validating \"deploy.yaml\": error validating data: kind not set\n"),
		"kustomize":     []byte("Error: accumulating resources: accumulation err='accumulating resources from 'base': no such file'\n"),
		"make":          []byte("make: *** [Makefile:12: manifests] Error 1\n"),
		"helm-warnings": append([]byte("WARNING: Kubernetes configuration file is group-readable. This is insecure.\n"), real...),
	}
}

// TestBrokenInputEachSide splits the shared-labels example into two files,
// breaks each in turn with the other real, and requires exit 3 with no
// result under every --fail-on.
func TestBrokenInputEachSide(t *testing.T) {
	src := string(read(t, "examples/shared-labels/rendered.yaml"))
	i := strings.Index(src, "---\napiVersion: apps/v1\nkind: Deployment")
	sides := map[string][]byte{"selectors": []byte(src[:i]), "workloads": []byte(src[i:])}
	for side := range sides {
		for name, data := range broken(t, sides[side]) {
			t.Run(side+"/"+name, func(t *testing.T) {
				dir := t.TempDir()
				for s, real := range sides {
					if s == side {
						real = data
					}
					if err := os.WriteFile(filepath.Join(dir, s+".yaml"), real, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				for _, extra := range [][]string{nil, {"--fail-on", "never"}, {"--fail-on", "error"}, {"--format", "json"}} {
					args := append(append([]string{}, extra...), dir)
					out, errs, code := run(t, nil, args...)
					if code != 3 || out != "" || !strings.Contains(errs, "can't trust the input") {
						t.Errorf("%v: exit %d, stdout %q, stderr %q", extra, code, out, errs)
					}
				}
			})
		}
	}
}

// TestEveryLineCut cuts the shared-labels example after each line. No cut
// may print a clean result or exit 0: either the input is refused, or
// findings remain, or a selector that matches nothing is reported.
func TestEveryLineCut(t *testing.T) {
	src := read(t, "examples/shared-labels/rendered.yaml")
	lines := bytes.SplitAfter(src, []byte("\n"))
	dir := t.TempDir()
	for n := 1; n < len(lines)-1; n++ {
		p := filepath.Join(dir, "cut.yaml")
		if err := os.WriteFile(p, bytes.Join(lines[:n], nil), 0o600); err != nil {
			t.Fatal(err)
		}
		out, _, code := run(t, nil, p)
		if code == 0 || strings.Contains(out, clean) {
			t.Errorf("cut after line %d: exit %d\n%s", n, code, out)
		}
	}
}

// TestCutInsideHealthy cuts the healthy example inside objects. A cut that
// leaves only complete, valid objects can't be detected; these can.
func TestCutInsideHealthy(t *testing.T) {
	src := string(read(t, "examples/healthy/rendered.yaml"))
	dir := t.TempDir()
	cuts := map[string]string{
		"service selector": "    app.kubernetes.io/name: api\n    app.kubernetes.io/instance: demo\n",
		"pdb":              "spec:\n  minAvailable: 1\n",
		"deployment":       "      labels:\n        app.kubernetes.io/name: api\n",
		"container ports":  "          image: ghcr.io/example/api:1.4.0\n          ports:\n",
		"hpa":              "    name: demo-api\n  minReplicas: 3\n",
	}
	for name, marker := range cuts {
		i := strings.Index(src, marker)
		if i < 0 {
			t.Fatalf("%s: marker not found", name)
		}
		p := filepath.Join(dir, "cut.yaml")
		if err := os.WriteFile(p, []byte(src[:i+len(marker)]), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, extra := range [][]string{nil, {"--fail-on", "never"}} {
			out, _, code := run(t, nil, append(extra, p)...)
			if strings.Contains(out, clean) || (code == 0 && extra == nil) {
				t.Errorf("%s %v: exit %d\n%s", name, extra, code, out)
			}
		}
	}
}

func TestNothingToCompare(t *testing.T) {
	dir := t.TempDir()
	cm := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\ndata:\n  k: v\n"
	one := string(read(t, "examples/healthy/rendered.yaml"))
	one = one[strings.Index(one, "apiVersion: apps/v1"):strings.Index(one, "---\napiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: demo-api-worker")]
	for name, data := range map[string]string{"configmap only": cm, "one deployment": one} {
		p := filepath.Join(dir, "m.yaml")
		if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, extra := range [][]string{nil, {"--fail-on", "never"}} {
			if out, _, code := run(t, nil, append(extra, p)...); code != 3 || out != "" {
				t.Errorf("%s %v: exit %d %q", name, extra, code, out)
			}
		}
	}
	if _, _, code := run(t, nil, filepath.Join(dir, "*.json")); code != 3 {
		t.Errorf("glob with no match: exit %d", code)
	}
	if _, _, code := run(t, nil, t.TempDir()); code != 3 {
		t.Errorf("directory with no manifests: exit %d", code)
	}
	if _, _, code := run(t, nil, filepath.Join(dir, "missing.yaml")); code != 3 {
		t.Errorf("missing file: exit %d", code)
	}
	if _, _, code := run(t, []byte{}, "-"); code != 3 {
		t.Errorf("empty stdin: exit %d", code)
	}
}
